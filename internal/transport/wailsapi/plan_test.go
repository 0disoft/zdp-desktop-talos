package wailsapi

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/modelruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/planning"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/workspace"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
)

func TestPlanServiceRequiresExactCurrentEgressConsent(t *testing.T) {
	t.Parallel()
	database, vaultService, workspaceService, snapshot := planServiceFixture(t)
	factory := &planningFactoryStub{ready: true, provider: "openai-responses", model: "model-test", proposer: &planProposerStub{}}
	service := NewPlanService(vaultService, workspaceService, factory)

	withoutConsent := service.Propose(PlanRequest{TaskID: database.taskCreated.Task.ID, RequestID: "request-1"})
	if withoutConsent.Error == nil || withoutConsent.Error.Code != "MODEL_EGRESS_CONSENT_REQUIRED" || factory.newCalls != 0 {
		t.Fatalf("result=%+v newCalls=%d", withoutConsent, factory.newCalls)
	}
	stale := service.Propose(PlanRequest{
		TaskID: database.taskCreated.Task.ID, RequestID: "request-2",
		Consent: PlanConsent{Confirmed: true, ProviderKey: factory.provider, ModelKey: factory.model, WorkspaceRoot: snapshot.Root, BaselineCommit: strings.Repeat("b", 40), ContractRevision: 1},
	})
	if stale.Error == nil || stale.Error.Code != "MODEL_CONFIGURATION_CHANGED" || factory.newCalls != 0 {
		t.Fatalf("result=%+v newCalls=%d", stale, factory.newCalls)
	}
}

func TestPlanServiceReturnsReviewOnlyProposal(t *testing.T) {
	t.Parallel()
	database, vaultService, workspaceService, snapshot := planServiceFixture(t)
	now := time.Date(2026, 7, 18, 8, 0, 0, 0, time.UTC)
	proposer := &planProposerStub{result: modelruntime.Result{
		State:   modelruntime.StateProposed,
		Plan:    planning.Plan{SchemaVersion: 1, Summary: "Run the focused verification first.", Steps: []planning.Step{{ID: "verify", Purpose: "Run focused tests", Tool: planning.ToolIntent{ID: "verify-tool", Kind: planning.ToolVerificationCommand, CommandIndex: 0}}}},
		Receipt: planning.EgressReceipt{ID: "receipt-1", TaskID: database.taskCreated.Task.ID, ContractRevision: 1, ProviderCallID: "resp_1", ContextItems: 2, InputBytes: 100, OutputBytes: 80, RedactionCount: 1, Usage: planning.Usage{InputTokens: 20, CachedInputTokens: 5, OutputTokens: 10}, CreatedAt: now, UpdatedAt: now},
	}}
	factory := &planningFactoryStub{ready: true, provider: "openai-responses", model: "model-test", proposer: proposer}
	result := NewPlanService(vaultService, workspaceService, factory).Propose(PlanRequest{
		TaskID: database.taskCreated.Task.ID, RequestID: "request-proposal", CorrelationID: "plan-1",
		Consent: PlanConsent{Confirmed: true, ProviderKey: factory.provider, ModelKey: factory.model, WorkspaceRoot: snapshot.Root, BaselineCommit: snapshot.BaselineCommit, ContractRevision: 1},
	})
	if result.Error != nil || result.Proposal == nil || result.Proposal.State != "proposed" || result.Proposal.Summary != "Run the focused verification first." || len(result.Proposal.Steps) != 1 || result.Proposal.Steps[0].CommandIndex != 0 {
		t.Fatalf("result=%+v", result)
	}
	if factory.newCalls != 1 || proposer.request.TaskID != database.taskCreated.Task.ID || proposer.request.IdempotencyKey != "plan-proposal:request-proposal" {
		t.Fatalf("factory=%+v request=%+v", factory, proposer.request)
	}
}

func TestPlanServiceSurfacesConfigurationWithoutCredentialValue(t *testing.T) {
	t.Parallel()
	service := NewPlanService(nil, nil, &planningFactoryStub{provider: "openai-responses", credential: "OPENAI_API_KEY", reason: "MODEL_NAME_UNCONFIGURED"})
	status := service.ProviderStatus()
	if status.Ready || status.ProviderKey != "openai-responses" || status.CredentialName != "OPENAI_API_KEY" || status.ReasonCode != "MODEL_NAME_UNCONFIGURED" || status.ModelKey != "" {
		t.Fatalf("status=%+v", status)
	}
}

func planServiceFixture(t *testing.T) (*serviceDatabase, *VaultService, *WorkspaceService, workspace.RepositorySnapshot) {
	t.Helper()
	now := time.Date(2026, 7, 18, 7, 0, 0, 0, time.UTC)
	snapshot := workspace.RepositorySnapshot{Root: `C:\repo`, BaselineCommit: strings.Repeat("a", 40), HeadRef: "main", CapturedAt: now}
	database := &serviceDatabase{}
	vaultService := openTaskTestVault(t, database)
	database.taskCreated = taskstore.Created{
		Task:     task.Record{ID: "task-1", VaultID: database.record.ID, WorkspaceRoot: snapshot.Root, BaselineCommit: snapshot.BaselineCommit, Status: task.StatusContracted, CurrentRevision: 1, CreatedAt: now, UpdatedAt: now, LastEventID: "task-event"},
		Contract: task.ContractRevision{TaskID: "task-1", Revision: 1, BaselineCommit: snapshot.BaselineCommit, Goal: "Verify repository", AllowedPaths: []string{"internal/**"}, AcceptanceCriteria: []string{"tests pass"}, VerificationCommands: []task.VerificationCommand{{RuleID: "go-test", Arguments: []string{"test", "./..."}, WorkingDirectory: "."}}, Risk: task.RiskLow, CreatedAt: now, EventID: "contract-event"},
	}
	workspaceService := NewWorkspaceService(&sequenceInspector{snapshots: []workspace.RepositorySnapshot{snapshot, snapshot, snapshot}}, nil)
	if result := workspaceService.InspectRepository(snapshot.Root, "open"); result.Error != nil {
		t.Fatalf("open workspace=%+v", result)
	}
	return database, vaultService, workspaceService, snapshot
}

type planningFactoryStub struct {
	provider   string
	model      string
	credential string
	reason     string
	ready      bool
	proposer   modelruntime.Proposer
	err        error
	newCalls   int
}

func (f *planningFactoryStub) ProviderConfiguration() (string, string, string, string, bool) {
	return f.provider, f.model, f.credential, f.reason, f.ready
}

func (f *planningFactoryStub) New(vaultbootstrap.PlanningDatabase) (modelruntime.Proposer, error) {
	f.newCalls++
	return f.proposer, f.err
}

type planProposerStub struct {
	request modelruntime.Request
	result  modelruntime.Result
	err     error
}

func (p *planProposerStub) Propose(_ context.Context, request modelruntime.Request) (modelruntime.Result, error) {
	p.request = request
	return p.result, p.err
}

func (*serviceDatabase) PrepareModelEgress(context.Context, modelstore.PrepareInput) (planning.EgressReceipt, error) {
	return planning.EgressReceipt{}, modelstore.ErrNotFound
}

func (*serviceDatabase) FinishModelEgress(context.Context, modelstore.FinishInput) (planning.EgressReceipt, error) {
	return planning.EgressReceipt{}, modelstore.ErrNotFound
}

func (*serviceDatabase) GetModelEgress(context.Context, string) (planning.EgressReceipt, error) {
	return planning.EgressReceipt{}, modelstore.ErrNotFound
}
