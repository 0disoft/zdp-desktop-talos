package scenario_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/fixturemodel"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/gitcli"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/application/contextassembly"
	"github.com/0disoft/zdp-desktop-talos/internal/application/executionruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/memorycompile"
	"github.com/0disoft/zdp-desktop-talos/internal/application/memorykernel"
	"github.com/0disoft/zdp-desktop-talos/internal/application/modelruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/patchcommand"
	"github.com/0disoft/zdp-desktop-talos/internal/application/patchreview"
	"github.com/0disoft/zdp-desktop-talos/internal/application/permissionbroker"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelprovider"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/workerruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

func TestDeterministicTaskDecisionPatchAndMemoryLoop(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 8, 15, 1, 0, 0, 0, time.UTC)
	git, repositoryRoot, baseline := createRepository(t)

	sealer, err := envelope.NewSealer("scenario-key", bytes.Repeat([]byte{0x37}, 32))
	if err != nil {
		t.Fatal(err)
	}
	defer sealer.Destroy()
	store, err := sqliteevent.Open(filepath.Join(t.TempDir(), "scenario.db"), sealer)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	vaultID := "00000000-0000-7000-8000-000000000101"
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: vaultID, RetentionDays: 30, OccurredAt: now, IdempotencyKey: "scenario-vault"}); err != nil {
		t.Fatal(err)
	}

	first := createTask(t, ctx, store, vaultID, repositoryRoot, baseline, now.Add(time.Second), "scenario-task-1")
	decisionResult, err := store.CreateDecision(ctx, decisionstore.CreateInput{
		VaultID: vaultID, TaskID: first.Task.ID, Category: decision.CategoryQuality, ExpectedRepositoryRevision: baseline,
		Question: "How should stale Decision answers be handled?", Reason: "The workspace needs one deterministic revision rule.",
		RiskIfUnanswered: "A stale answer could silently change the patch.", SafeDefault: decision.SafeDefault{Action: "reject stale answers"},
		Options:    []decision.Option{{ID: "strict", Label: "Reject stale answers", Consequence: "Require the exact question revision."}, {ID: "latest", Label: "Use the newest answer", Consequence: "Allow later answers to replace earlier ones."}},
		OccurredAt: now.Add(2 * time.Second), IdempotencyKey: "scenario-decision",
	})
	if err != nil {
		t.Fatal(err)
	}
	answered, err := store.AnswerDecision(ctx, decisionstore.AnswerInput{
		VaultID: vaultID, DecisionID: decisionResult.Decision.ID, QuestionRevision: 1, ExpectedRepositoryRevision: baseline,
		SelectedOptionID: "strict", OccurredAt: now.Add(3 * time.Second), IdempotencyKey: "scenario-answer",
	})
	if err != nil || answered.Decision.State != decision.StateAnswered {
		t.Fatalf("answered=%+v error=%v", answered, err)
	}

	worktrees, err := gitcli.NewWorktreeManager(filepath.Join(t.TempDir(), "talos-owned"))
	if err != nil {
		t.Fatal(err)
	}
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	broker, err := permissionbroker.New([]permissionbroker.ProcessRule{{
		ID: "fixture-verify", Executable: executable, ArgumentPrefix: []string{"verify"}, MaxArguments: 1,
		MaxTimeout: time.Second, MaxOutputBytes: 4096, Default: permission.OutcomeAllowTask,
	}})
	if err != nil {
		t.Fatal(err)
	}
	workers := &repairWorkerFactory{}
	coordinator, err := executionruntime.New(store, broker, worktrees, workers)
	if err != nil {
		t.Fatal(err)
	}
	assembler, err := contextassembly.New(store)
	if err != nil {
		t.Fatal(err)
	}
	provider, err := fixturemodel.New("fixture", "fixture-plan-v1", []int{0})
	if err != nil {
		t.Fatal(err)
	}
	runtime := newModelRuntime(t, store, provider, coordinator, assembler)

	failed, err := runtime.Run(ctx, modelruntime.Request{TaskID: first.Task.ID, RequestID: "scenario-request-1", IdempotencyKey: "scenario-run-1"})
	if !errors.Is(err, modelruntime.ErrExecutionIncomplete) || len(failed.Executions) != 1 || failed.Executions[0].Evidence != nil {
		t.Fatalf("failed=%+v error=%v", failed, err)
	}
	succeeded, err := runtime.Run(ctx, modelruntime.Request{TaskID: first.Task.ID, RequestID: "scenario-request-2", IdempotencyKey: "scenario-run-2"})
	if err != nil || succeeded.State != modelruntime.StateCompleted || len(succeeded.Executions) != 1 || succeeded.Executions[0].Evidence == nil || workers.attempts != 2 {
		t.Fatalf("succeeded=%+v attempts=%d error=%v", succeeded, workers.attempts, err)
	}

	reviewer, err := patchreview.New(store, worktrees, redaction.NewScanner())
	if err != nil {
		t.Fatal(err)
	}
	review, err := reviewer.Get(ctx, first.Task.ID)
	if err != nil || review.Status != patchreview.StatusFresh || len(review.Changes) != 1 || review.Changes[0].Path != "internal/result.txt" || review.SecretFindings != 0 {
		t.Fatalf("review=%+v error=%v", review, err)
	}
	patches, err := patchcommand.New(store, reviewer, worktrees)
	if err != nil {
		t.Fatal(err)
	}
	applied, err := patches.Execute(ctx, patchcommand.Request{
		TaskID: first.Task.ID, Kind: patchaction.KindApply, ExpectedRevision: first.Task.CurrentRevision, ExpectedPatchHash: review.PatchHash,
		IdempotencyKey: "scenario-apply", WorkspaceRoot: repositoryRoot, BaselineCommit: baseline,
	})
	if err != nil || applied.TaskStatus != task.StatusCompleted {
		t.Fatalf("applied=%+v error=%v", applied, err)
	}
	content, err := os.ReadFile(filepath.Join(repositoryRoot, "internal", "result.txt"))
	if err != nil || string(content) != "fixed\n" {
		t.Fatalf("primary content=%q error=%v", content, err)
	}

	compiler, err := memorycompile.New(store, store, store, redaction.NewScanner())
	if err != nil {
		t.Fatal(err)
	}
	compiled, err := compiler.CompileTask(ctx, vaultID, first.Task.ID)
	if err != nil || compiled.Eligible != 1 || len(compiled.Memories) != 1 {
		t.Fatalf("compiled=%+v error=%v", compiled, err)
	}
	kernel, err := memorykernel.New(store)
	if err != nil {
		t.Fatal(err)
	}
	approved, err := kernel.ReviewCandidate(ctx, memorystore.TransitionInput{
		VaultID: vaultID, MemoryID: compiled.Memories[0].ID, ExpectedRevision: 1, NextState: memory.StateApproved,
		Reason: "scenario user approval", OccurredAt: now.Add(8 * time.Second), IdempotencyKey: "scenario-memory-approve",
	})
	if err != nil {
		t.Fatal(err)
	}

	runGit(t, git, repositoryRoot, "add", "internal/result.txt")
	runGit(t, git, repositoryRoot, "commit", "-m", "accept Talos patch")
	nextBaseline := gitOutput(t, git, repositoryRoot, "rev-parse", "HEAD")
	second := createTask(t, ctx, store, vaultID, repositoryRoot, nextBaseline, now.Add(9*time.Second), "scenario-task-2")
	next, err := runtime.Propose(ctx, modelruntime.Request{TaskID: second.Task.ID, RequestID: "scenario-request-3", IdempotencyKey: "scenario-run-3"})
	if err != nil || len(next.MemoryContext.Items) != 1 || next.MemoryContext.Items[0].MemoryID != approved.ID || next.MemoryContext.Items[0].Reason == "" || !strings.Contains(next.Plan.Summary, approved.Statement) {
		t.Fatalf("next=%+v approved=%+v error=%v", next, approved, err)
	}
}

func createTask(t *testing.T, ctx context.Context, store *sqliteevent.Store, vaultID, root, baseline string, occurredAt time.Time, key string) taskstore.Created {
	t.Helper()
	created, err := store.CreateTaskContract(ctx, taskstore.CreateInput{
		VaultID: vaultID, WorkspaceRoot: root, BaselineCommit: baseline, Goal: "keep Decision revision verification strict",
		AllowedPaths: []string{"internal/**"}, ForbiddenActions: []string{"git.push"}, AcceptanceCriteria: []string{"focused verification passes"},
		VerificationCommands: []task.VerificationCommand{{RuleID: "fixture-verify", Arguments: []string{"verify"}, WorkingDirectory: "."}},
		Risk:                 task.RiskMedium, OccurredAt: occurredAt, IdempotencyKey: key,
	})
	if err != nil {
		t.Fatal(err)
	}
	return created
}

func newModelRuntime(t *testing.T, store *sqliteevent.Store, provider modelprovider.Provider, executor executionruntime.Executor, assembler *contextassembly.Service) *modelruntime.Service {
	t.Helper()
	runtime, err := modelruntime.New(store, provider, redaction.NewScanner(), executor, modelruntime.Policy{
		ProviderKey: "fixture", ModelKey: "fixture-plan-v1", PromptVersion: modelruntime.PlanningPromptVersion,
		MaxContextItems: 8, MaxMemoryCandidates: 32, MaxMemoryItems: 4, MaxMemoryBytes: 8 << 10, MaxInputBytes: 64 << 10,
		MaxOutputBytes: 16 << 10, MaxSteps: 4, MaxToolIntents: 4, ProviderTimeout: time.Second,
	}, modelruntime.WithMemoryContext(assembler))
	if err != nil {
		t.Fatal(err)
	}
	return runtime
}

type repairWorkerFactory struct {
	attempts int
}

func (f *repairWorkerFactory) Start(context.Context) (workerruntime.Session, error) {
	return &repairWorkerSession{factory: f}, nil
}

type repairWorkerSession struct {
	factory *repairWorkerFactory
	root    string
}

func (s *repairWorkerSession) StartRun(_ context.Context, policy workerruntime.RunPolicy) error {
	if policy.RunID == "" || policy.WorktreeRoot == "" || len(policy.Capabilities) != 1 {
		return workerruntime.ErrProtocol
	}
	s.root = policy.WorktreeRoot
	return nil
}

func (s *repairWorkerSession) RunTool(_ context.Context, request workerruntime.ToolRequest) (workerruntime.ToolResult, error) {
	s.factory.attempts++
	content := "broken\n"
	state, exitCode, runErr := workerruntime.ToolFailed, 1, workerruntime.ErrExecutionFailed
	if s.factory.attempts == 2 {
		content, state, exitCode, runErr = "fixed\n", workerruntime.ToolSucceeded, 0, nil
	}
	if s.root == "" || request.RunID == "" || request.CallID == "" || request.CapabilityID == "" {
		return workerruntime.ToolResult{}, workerruntime.ErrProtocol
	}
	if err := os.WriteFile(filepath.Join(s.root, "internal", "result.txt"), []byte(content), 0o600); err != nil {
		return workerruntime.ToolResult{}, err
	}
	started := time.Now().UTC()
	stdout := sha256.Sum256([]byte(content))
	return workerruntime.ToolResult{State: state, ExitCode: exitCode, StdoutBytes: len(content), StdoutSHA256: hex.EncodeToString(stdout[:]), StderrSHA256: strings.Repeat("0", 64), StartedAt: started, FinishedAt: started.Add(time.Millisecond)}, runErr
}

func (*repairWorkerSession) Shutdown(context.Context) error { return nil }
func (*repairWorkerSession) Close() error                   { return nil }

func createRepository(t *testing.T) (string, string, string) {
	t.Helper()
	git, err := exec.LookPath("git")
	if err != nil {
		t.Skip("Git executable unavailable")
	}
	root := filepath.Join(t.TempDir(), "repository")
	if err := os.MkdirAll(filepath.Join(root, "internal"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "internal", "result.txt"), []byte("baseline\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "README.md"), []byte("# Scenario fixture\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	runGit(t, git, root, "init", "--initial-branch=main")
	runGit(t, git, root, "config", "user.name", "Talos Scenario")
	runGit(t, git, root, "config", "user.email", "talos-scenario@example.invalid")
	runGit(t, git, root, "config", "commit.gpgsign", "false")
	runGit(t, git, root, "config", "core.autocrlf", "false")
	runGit(t, git, root, "add", ".")
	runGit(t, git, root, "commit", "-m", "initial fixture")
	return git, root, gitOutput(t, git, root, "rev-parse", "HEAD")
}

func runGit(t *testing.T, git, root string, arguments ...string) {
	t.Helper()
	command := exec.Command(git, append([]string{"-C", root}, arguments...)...)
	if output, err := command.CombinedOutput(); err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
}

func gitOutput(t *testing.T, git, root string, arguments ...string) string {
	t.Helper()
	command := exec.Command(git, append([]string{"-C", root}, arguments...)...)
	output, err := command.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s: %v\n%s", strings.Join(arguments, " "), err, output)
	}
	return strings.TrimSpace(string(output))
}
