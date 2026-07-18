package vaultbootstrap

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/syncexport"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncidentity"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncpack"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/artifact"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/redaction"
)

const (
	VaultKeyID    = "vault-kek-v1"
	vaultKeyBytes = 32
)

var (
	ErrInvalidInput          = errors.New("invalid Vault creation input")
	ErrCompensationFailed    = errors.New("Vault creation failed and cleanup was incomplete")
	ErrNotCataloged          = errors.New("Vault is not present in the protected catalog")
	ErrNotOpen               = errors.New("Vault session is not open")
	ErrPurgeIncomplete       = errors.New("Vault hard purge is pending recovery")
	ErrTaskWorkspaceMismatch = errors.New("task does not belong to the current workspace snapshot")
)

type CreateInput struct {
	RetentionDays int
}

type UpdateRetentionInput struct {
	ExpectedRevision int
	RetentionDays    int
	IdempotencyKey   string
}

type StoreArtifactInput struct {
	SchemaVersion int
	Sensitivity   event.Sensitivity
	ContentType   string
	Payload       []byte
}

type CreateTaskContractInput struct {
	WorkspaceRoot        string
	BaselineCommit       string
	Goal                 string
	AllowedPaths         []string
	ForbiddenActions     []string
	AcceptanceCriteria   []string
	VerificationCommands []task.VerificationCommand
	Risk                 task.Risk
	IdempotencyKey       string
}

type ReviseTaskContractInput struct {
	TaskID               string
	ExpectedRevision     int
	WorkspaceRoot        string
	BaselineCommit       string
	Goal                 string
	AllowedPaths         []string
	ForbiddenActions     []string
	AcceptanceCriteria   []string
	VerificationCommands []task.VerificationCommand
	Risk                 task.Risk
	IdempotencyKey       string
}

type CreateDecisionInput struct {
	TaskID                     string
	WorkspaceRoot              string
	ExpectedRepositoryRevision string
	Category                   decision.Category
	Question                   string
	Reason                     string
	RiskIfUnanswered           string
	SafeDefault                decision.SafeDefault
	BlockingScopes             []string
	Options                    []decision.Option
	IdempotencyKey             string
}

type AnswerDecisionInput struct {
	DecisionID                 string
	QuestionRevision           int
	ExpectedRepositoryRevision string
	SelectedOptionID           string
	Text                       string
	IdempotencyKey             string
}

type SupersedeDecisionInput struct {
	DecisionID                 string
	ExpectedQuestionRevision   int
	ExpectedRepositoryRevision string
	Question                   string
	Reason                     string
	RiskIfUnanswered           string
	SafeDefault                decision.SafeDefault
	BlockingScopes             []string
	Options                    []decision.Option
	IdempotencyKey             string
}

type ResolveDecisionConflictInput struct {
	DecisionID                 string
	QuestionRevision           int
	ExpectedRepositoryRevision string
	SelectedAnswerID           string
	IdempotencyKey             string
}

type HardPurgeInput struct {
	ExpectedRevision int
	Confirmation     string
}

type Session struct {
	Record   vault.Record
	database vaultdb.Database
	keys     keyvault.Store
}

type ExecutionDatabase interface {
	taskstore.Store
	executionstore.Store
	GetLatestVerificationEvidence(context.Context, string, string) (verification.Evidence, error)
}

type PatchDatabase interface {
	taskstore.Store
	patchstore.Store
	GetLatestVerificationEvidence(context.Context, string, string) (verification.Evidence, error)
}

type AccountDatabase interface {
	accountstore.Store
}

type MemoryDatabase interface {
	taskstore.Store
	decisionstore.Store
	memorystore.Store
}

type PlanningDatabase interface {
	ExecutionDatabase
	memorystore.Store
	modelstore.Store
}

func (s *Session) ExecutionDatabase() (ExecutionDatabase, error) {
	if s == nil || s.database == nil {
		return nil, ErrNotOpen
	}
	return s.database, nil
}

func (s *Session) PatchDatabase() (PatchDatabase, error) {
	if s == nil || s.database == nil {
		return nil, ErrNotOpen
	}
	return s.database, nil
}

func (s *Session) AccountDatabase() (AccountDatabase, error) {
	if s == nil || s.database == nil {
		return nil, ErrNotOpen
	}
	return s.database, nil
}

func (s *Session) MemoryDatabase() (MemoryDatabase, error) {
	if s == nil || s.database == nil {
		return nil, ErrNotOpen
	}
	database, ok := s.database.(MemoryDatabase)
	if !ok {
		return nil, ErrNotOpen
	}
	return database, nil
}

func (s *Session) PlanningDatabase() (PlanningDatabase, error) {
	if s == nil || s.database == nil {
		return nil, ErrNotOpen
	}
	database, ok := s.database.(PlanningDatabase)
	if !ok {
		return nil, ErrNotOpen
	}
	return database, nil
}

func (s *Session) ExportNextSyncPack(ctx context.Context, limit int) (syncexport.Result, error) {
	if s == nil || s.database == nil || s.keys == nil {
		return syncexport.Result{}, ErrNotOpen
	}
	exporter, err := syncexport.New(syncpack.New(), s.database, s.keys, redaction.NewScanner(), VaultKeyID)
	if err != nil {
		return syncexport.Result{}, err
	}
	return exporter.ExportNext(ctx, s.Record.ID, limit)
}

func (s *Session) Close() error {
	if s == nil || s.database == nil {
		return nil
	}
	err := s.database.Close()
	s.database = nil
	return err
}

func (s *Session) UpdateRetention(ctx context.Context, input UpdateRetentionInput) (vault.Record, error) {
	if s == nil || s.database == nil {
		return vault.Record{}, ErrNotOpen
	}
	if input.ExpectedRevision < 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return vault.Record{}, ErrInvalidInput
	}
	if err := vault.ValidateRetentionDays(input.RetentionDays); err != nil {
		return vault.Record{}, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	if input.ExpectedRevision != s.Record.Revision {
		return vault.Record{}, vaultstore.ErrRevisionConflict
	}
	record, err := s.database.UpdateVaultRetention(ctx, vaultstore.UpdateRetentionInput{
		VaultID:          s.Record.ID,
		ExpectedRevision: input.ExpectedRevision,
		RetentionDays:    input.RetentionDays,
		OccurredAt:       time.Now().UTC(),
		IdempotencyKey:   input.IdempotencyKey,
	})
	if err != nil {
		return vault.Record{}, err
	}
	s.Record = record
	return record, nil
}

func (s *Session) StoreArtifact(ctx context.Context, input StoreArtifactInput) (artifact.Record, error) {
	if s == nil || s.database == nil {
		return artifact.Record{}, ErrNotOpen
	}
	return s.database.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: s.Record.ID, SchemaVersion: input.SchemaVersion, Sensitivity: input.Sensitivity,
		ContentType: input.ContentType, Payload: input.Payload,
	})
}

func (s *Session) LoadArtifact(ctx context.Context, artifactID string) (artifact.Record, []byte, error) {
	if s == nil || s.database == nil {
		return artifact.Record{}, nil, ErrNotOpen
	}
	record, payload, err := s.database.GetArtifact(ctx, artifactID)
	if err != nil {
		return artifact.Record{}, nil, err
	}
	if record.VaultID != s.Record.ID {
		clear(payload)
		return artifact.Record{}, nil, artifactstore.ErrNotFound
	}
	return record, payload, nil
}

func (s *Session) CreateTaskContract(ctx context.Context, input CreateTaskContractInput) (taskstore.Created, error) {
	if s == nil || s.database == nil {
		return taskstore.Created{}, ErrNotOpen
	}
	if input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || len(input.VerificationCommands) == 0 {
		return taskstore.Created{}, ErrInvalidInput
	}
	return s.database.CreateTaskContract(ctx, taskstore.CreateInput{
		VaultID: s.Record.ID, WorkspaceRoot: input.WorkspaceRoot, BaselineCommit: input.BaselineCommit,
		Goal: input.Goal, AllowedPaths: input.AllowedPaths, ForbiddenActions: input.ForbiddenActions,
		AcceptanceCriteria: input.AcceptanceCriteria, VerificationCommands: input.VerificationCommands, Risk: input.Risk,
		IdempotencyKey: input.IdempotencyKey,
	})
}

func (s *Session) ReviseTaskContract(ctx context.Context, input ReviseTaskContractInput) (taskstore.Created, error) {
	if s == nil || s.database == nil {
		return taskstore.Created{}, ErrNotOpen
	}
	if input.TaskID == "" || input.ExpectedRevision < 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 || len(input.VerificationCommands) == 0 {
		return taskstore.Created{}, ErrInvalidInput
	}
	current, err := s.database.GetTask(ctx, input.TaskID)
	if err != nil {
		return taskstore.Created{}, err
	}
	if current.VaultID != s.Record.ID || current.WorkspaceRoot != input.WorkspaceRoot || current.BaselineCommit != input.BaselineCommit {
		return taskstore.Created{}, ErrTaskWorkspaceMismatch
	}
	return s.database.ReviseTaskContract(ctx, taskstore.ReviseInput{
		VaultID: s.Record.ID, TaskID: input.TaskID, ExpectedRevision: input.ExpectedRevision,
		Goal: input.Goal, AllowedPaths: input.AllowedPaths, ForbiddenActions: input.ForbiddenActions,
		AcceptanceCriteria: input.AcceptanceCriteria, VerificationCommands: input.VerificationCommands, Risk: input.Risk,
		IdempotencyKey: input.IdempotencyKey,
	})
}

func (s *Session) CreateDecision(ctx context.Context, input CreateDecisionInput) (decisionstore.Result, error) {
	if s == nil || s.database == nil {
		return decisionstore.Result{}, ErrNotOpen
	}
	if input.TaskID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return decisionstore.Result{}, ErrInvalidInput
	}
	current, err := s.database.GetTask(ctx, input.TaskID)
	if err != nil {
		return decisionstore.Result{}, err
	}
	if current.VaultID != s.Record.ID || current.WorkspaceRoot != input.WorkspaceRoot || current.BaselineCommit != input.ExpectedRepositoryRevision {
		return decisionstore.Result{}, ErrTaskWorkspaceMismatch
	}
	return s.database.CreateDecision(ctx, decisionstore.CreateInput{VaultID: s.Record.ID, TaskID: input.TaskID, Category: input.Category, ExpectedRepositoryRevision: input.ExpectedRepositoryRevision, Question: input.Question, Reason: input.Reason, RiskIfUnanswered: input.RiskIfUnanswered, SafeDefault: input.SafeDefault, BlockingScopes: input.BlockingScopes, Options: input.Options, IdempotencyKey: input.IdempotencyKey})
}

func (s *Session) ListDecisions(ctx context.Context, taskID string, limit int) ([]decisionstore.Result, error) {
	if s == nil || s.database == nil {
		return nil, ErrNotOpen
	}
	return s.database.ListDecisions(ctx, s.Record.ID, taskID, limit)
}

func (s *Session) AnswerDecision(ctx context.Context, input AnswerDecisionInput) (decisionstore.Result, error) {
	if s == nil || s.database == nil {
		return decisionstore.Result{}, ErrNotOpen
	}
	if input.DecisionID == "" || input.QuestionRevision < 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return decisionstore.Result{}, ErrInvalidInput
	}
	return s.database.AnswerDecision(ctx, decisionstore.AnswerInput{VaultID: s.Record.ID, DecisionID: input.DecisionID, QuestionRevision: input.QuestionRevision, ExpectedRepositoryRevision: input.ExpectedRepositoryRevision, SelectedOptionID: input.SelectedOptionID, Text: input.Text, IdempotencyKey: input.IdempotencyKey})
}

func (s *Session) SupersedeDecision(ctx context.Context, input SupersedeDecisionInput) (decisionstore.Result, error) {
	if s == nil || s.database == nil {
		return decisionstore.Result{}, ErrNotOpen
	}
	if input.DecisionID == "" || input.ExpectedQuestionRevision < 1 || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return decisionstore.Result{}, ErrInvalidInput
	}
	return s.database.SupersedeDecision(ctx, decisionstore.SupersedeInput{VaultID: s.Record.ID, DecisionID: input.DecisionID, ExpectedQuestionRevision: input.ExpectedQuestionRevision, ExpectedRepositoryRevision: input.ExpectedRepositoryRevision, Question: input.Question, Reason: input.Reason, RiskIfUnanswered: input.RiskIfUnanswered, SafeDefault: input.SafeDefault, BlockingScopes: input.BlockingScopes, Options: input.Options, IdempotencyKey: input.IdempotencyKey})
}

func (s *Session) ResolveDecisionConflict(ctx context.Context, input ResolveDecisionConflictInput) (decisionstore.Result, error) {
	if s == nil || s.database == nil {
		return decisionstore.Result{}, ErrNotOpen
	}
	if input.DecisionID == "" || input.QuestionRevision < 1 || input.SelectedAnswerID == "" || input.IdempotencyKey == "" || len(input.IdempotencyKey) > 128 {
		return decisionstore.Result{}, ErrInvalidInput
	}
	return s.database.ResolveDecisionConflict(ctx, decisionstore.ResolveInput{VaultID: s.Record.ID, DecisionID: input.DecisionID, QuestionRevision: input.QuestionRevision, ExpectedRepositoryRevision: input.ExpectedRepositoryRevision, SelectedAnswerID: input.SelectedAnswerID, IdempotencyKey: input.IdempotencyKey})
}

type Creator struct {
	keys      keyvault.Store
	databases vaultdb.Factory
	catalog   vaultcatalog.Catalog
	random    io.Reader
	now       func() time.Time
}

func NewCreator(keys keyvault.Store, databases vaultdb.Factory, catalog vaultcatalog.Catalog) (*Creator, error) {
	if keys == nil || databases == nil || catalog == nil {
		return nil, ErrInvalidInput
	}
	return &Creator{
		keys:      keys,
		databases: databases,
		catalog:   catalog,
		random:    rand.Reader,
		now:       func() time.Time { return time.Now().UTC() },
	}, nil
}

func (c *Creator) Create(ctx context.Context, input CreateInput) (*Session, error) {
	if err := vault.ValidateRetentionDays(input.RetentionDays); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidInput, err)
	}
	now := c.now().UTC()
	vaultID, err := id.UUIDv7(now, c.random)
	if err != nil {
		return nil, fmt.Errorf("generate Vault ID: %w", err)
	}
	key := make([]byte, vaultKeyBytes)
	if _, err := io.ReadFull(c.random, key); err != nil {
		return nil, fmt.Errorf("generate Vault key: %w", err)
	}
	defer clear(key)

	keyRef := keyvault.Reference{VaultID: vaultID, KeyID: VaultKeyID}
	if err := c.keys.Put(ctx, keyRef, key); err != nil {
		return nil, fmt.Errorf("persist Vault key: %w", err)
	}

	database, err := c.databases.Create(ctx, vaultID, VaultKeyID, key)
	if err != nil {
		return nil, c.compensate(ctx, fmt.Errorf("create Vault database: %w", err), vaultcatalog.Entry{}, keyRef, nil)
	}
	record, err := database.CreateVault(ctx, vaultstore.CreateInput{
		VaultID:        vaultID,
		RetentionDays:  input.RetentionDays,
		OccurredAt:     now,
		IdempotencyKey: "vault-bootstrap:" + vaultID,
	})
	if err != nil {
		return nil, c.compensate(ctx, fmt.Errorf("initialize Vault state: %w", err), vaultcatalog.Entry{}, keyRef, database)
	}
	entry := vaultcatalog.Entry{VaultID: vaultID, CreatedAt: record.CreatedAt, State: vaultcatalog.StateActive}
	if err := c.catalog.Add(ctx, entry); err != nil {
		return nil, c.compensate(ctx, fmt.Errorf("register Vault in catalog: %w", err), entry, keyRef, database)
	}
	return &Session{Record: record, database: database, keys: c.keys}, nil
}

func (c *Creator) List(ctx context.Context) ([]vaultcatalog.Entry, error) {
	return c.catalog.List(ctx)
}

func (c *Creator) Open(ctx context.Context, vaultID string) (*Session, error) {
	if !id.IsUUIDv7(vaultID) {
		return nil, ErrNotCataloged
	}
	entries, err := c.catalog.List(ctx)
	if err != nil {
		return nil, fmt.Errorf("read protected Vault catalog: %w", err)
	}
	found := false
	for _, entry := range entries {
		if entry.VaultID == vaultID {
			found = true
			break
		}
	}
	if !found {
		return nil, ErrNotCataloged
	}
	keyRef := keyvault.Reference{VaultID: vaultID, KeyID: VaultKeyID}
	key, err := c.keys.Get(ctx, keyRef)
	if err != nil {
		return nil, fmt.Errorf("load Vault key: %w", err)
	}
	defer clear(key)
	database, err := c.databases.Open(ctx, vaultID, VaultKeyID, key)
	if err != nil {
		return nil, fmt.Errorf("open Vault database: %w", err)
	}
	record, err := database.GetVault(ctx, vaultID)
	if err != nil {
		_ = database.Close()
		return nil, fmt.Errorf("load Vault state: %w", err)
	}
	if record.ID != vaultID || record.Status != vault.StatusActive {
		_ = database.Close()
		return nil, fmt.Errorf("%w: stored Vault identity or state is invalid", ErrNotCataloged)
	}
	return &Session{Record: record, database: database, keys: c.keys}, nil
}

func (c *Creator) HardPurge(ctx context.Context, session *Session, input HardPurgeInput) error {
	if session == nil || session.database == nil {
		return ErrNotOpen
	}
	if input.ExpectedRevision != session.Record.Revision {
		return vaultstore.ErrRevisionConflict
	}
	if input.Confirmation != session.Record.ID {
		return ErrInvalidInput
	}
	entry := vaultcatalog.Entry{VaultID: session.Record.ID, CreatedAt: session.Record.CreatedAt, State: vaultcatalog.StateActive}
	if err := session.Close(); err != nil {
		return fmt.Errorf("close Vault before hard purge: %w", err)
	}
	if err := c.catalog.MarkPurgePending(ctx, entry); err != nil {
		return fmt.Errorf("record hard purge intent: %w", err)
	}
	entry.State = vaultcatalog.StatePurgePending
	if err := c.finishPurge(context.WithoutCancel(ctx), entry); err != nil {
		return errors.Join(ErrPurgeIncomplete, err)
	}
	return nil
}

func (c *Creator) ReconcilePurges(ctx context.Context) error {
	entries, err := c.catalog.PendingPurges(ctx)
	if err != nil {
		return fmt.Errorf("read pending Vault purges: %w", err)
	}
	var purgeErrors []error
	for _, entry := range entries {
		if err := c.finishPurge(context.WithoutCancel(ctx), entry); err != nil {
			purgeErrors = append(purgeErrors, fmt.Errorf("resume purge for Vault %s: %w", entry.VaultID, err))
		}
	}
	if len(purgeErrors) > 0 {
		return errors.Join(ErrPurgeIncomplete, errors.Join(purgeErrors...))
	}
	return nil
}

func (c *Creator) finishPurge(ctx context.Context, entry vaultcatalog.Entry) error {
	for _, keyID := range []string{VaultKeyID, syncidentity.SigningKeyID} {
		keyRef := keyvault.Reference{VaultID: entry.VaultID, KeyID: keyID}
		if err := c.keys.Delete(ctx, keyRef); err != nil && !errors.Is(err, keyvault.ErrNotFound) {
			return fmt.Errorf("destroy Vault key %s: %w", keyID, err)
		}
	}
	if err := c.databases.Purge(ctx, entry.VaultID); err != nil {
		return fmt.Errorf("remove Vault ciphertext: %w", err)
	}
	if err := c.catalog.Remove(ctx, entry); err != nil && !errors.Is(err, vaultcatalog.ErrNotFound) {
		return fmt.Errorf("complete Vault purge journal: %w", err)
	}
	return nil
}

func (c *Creator) compensate(ctx context.Context, cause error, entry vaultcatalog.Entry, keyRef keyvault.Reference, database vaultdb.Database) error {
	cleanupCtx := context.WithoutCancel(ctx)
	var cleanupErrors []error
	if !entry.CreatedAt.IsZero() {
		if err := c.catalog.Remove(cleanupCtx, entry); err != nil && !errors.Is(err, vaultcatalog.ErrNotFound) {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("remove partial Vault catalog entry: %w", err))
		}
	}
	if database != nil {
		if err := database.Close(); err != nil {
			cleanupErrors = append(cleanupErrors, fmt.Errorf("close partial Vault database: %w", err))
		}
	}
	if err := c.databases.Remove(cleanupCtx, keyRef.VaultID); err != nil && !errors.Is(err, vaultdb.ErrNotFound) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove partial Vault database: %w", err))
	}
	if err := c.keys.Delete(cleanupCtx, keyRef); err != nil && !errors.Is(err, keyvault.ErrNotFound) {
		cleanupErrors = append(cleanupErrors, fmt.Errorf("remove partial Vault key: %w", err))
	}
	if len(cleanupErrors) == 0 {
		return cause
	}
	return errors.Join(ErrCompensationFailed, cause, errors.Join(cleanupErrors...))
}
