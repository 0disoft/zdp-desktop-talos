package vaultbootstrap

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"errors"
	"fmt"
	"io"
	"time"

	enrollmentapp "github.com/0disoft/zdp-desktop-talos/internal/application/syncenrollment"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncexport"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncidentity"
	"github.com/0disoft/zdp-desktop-talos/internal/application/syncpack"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/artifact"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncenrollment"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/enrollmentstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
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
	ErrEnrollmentConflict    = errors.New("sync enrollment conflicts with local Vault state")
	ErrEnrollmentUnsupported = errors.New("Vault database does not support sync enrollment")
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

type CreateEnrollmentOfferInput struct {
	ValidFor time.Duration
}

type EnrollmentOffer struct {
	EnrollmentID string
	Encoded      []byte
	Secret       string
	ExpiresAt    time.Time
}

type AcceptEnrollmentOfferInput struct {
	Encoded []byte
	Secret  string
}

type AcceptedEnrollment struct {
	Session           *Session
	EnrollmentID      string
	EncodedAcceptance []byte
	SourceDeviceID    string
	TargetDeviceID    string
	Replay            bool
}

type CompleteEnrollmentInput struct {
	EncodedAcceptance []byte
	Secret            string
}

type ImportSyncPackInput struct {
	Encoded    []byte
	DeviceID   string
	ReceivedAt time.Time
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

type EnrollmentDatabase interface {
	syncstore.Store
	syncstore.ReplayStore
	enrollmentstore.Store
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

func (s *Session) CreateEnrollmentOffer(ctx context.Context, input CreateEnrollmentOfferInput) (EnrollmentOffer, error) {
	database, err := s.enrollmentDatabase()
	if err != nil || s.keys == nil || ctx == nil {
		return EnrollmentOffer{}, ErrNotOpen
	}
	identity, err := syncidentity.New(s.keys, database)
	if err != nil {
		return EnrollmentOffer{}, err
	}
	local, err := identity.Ensure(ctx, s.Record.ID)
	if err != nil {
		return EnrollmentOffer{}, err
	}
	defer clear(local.PrivateKey)
	vaultKey, err := s.keys.Get(ctx, keyvault.Reference{VaultID: s.Record.ID, KeyID: VaultKeyID})
	if err != nil {
		return EnrollmentOffer{}, err
	}
	defer clear(vaultKey)
	codec := enrollmentapp.NewCodec()
	created, err := codec.CreateOffer(ctx, enrollmentapp.CreateOfferInput{VaultID: s.Record.ID, VaultCreatedAt: s.Record.CreatedAt, RetentionDays: s.Record.RetentionDays, VaultKey: vaultKey, SourceDeviceID: local.Device.DeviceID, SourcePublicKey: local.Device.PublicKey, SigningKey: local.PrivateKey, IssuedAt: time.Now().UTC(), ValidFor: input.ValidFor})
	if err != nil {
		return EnrollmentOffer{}, err
	}
	defer clear(created.Offer.VaultKey)
	if _, _, err := database.RecordEnrollmentOffer(ctx, enrollmentstore.RecordOfferInput{EnrollmentID: created.Offer.EnrollmentID, VaultID: created.Offer.VaultID, OfferHash: created.Hash, ExpiresAt: created.Offer.ExpiresAt, OccurredAt: created.Offer.IssuedAt}); err != nil {
		return EnrollmentOffer{}, err
	}
	return EnrollmentOffer{EnrollmentID: created.Offer.EnrollmentID, Encoded: created.Encoded, Secret: created.Secret, ExpiresAt: created.Offer.ExpiresAt}, nil
}

func (s *Session) CompleteEnrollment(ctx context.Context, input CompleteEnrollmentInput) (syncstate.Device, error) {
	database, err := s.enrollmentDatabase()
	if err != nil || s.keys == nil || ctx == nil {
		return syncstate.Device{}, ErrNotOpen
	}
	codec := enrollmentapp.NewCodec()
	acceptance, acceptanceHash, err := codec.OpenAcceptance(ctx, input.EncodedAcceptance, input.Secret)
	if err != nil {
		return syncstate.Device{}, err
	}
	if acceptance.VaultID != s.Record.ID {
		return syncstate.Device{}, ErrEnrollmentConflict
	}
	stored, _, err := database.GetEnrollment(ctx, s.Record.ID, acceptance.EnrollmentID)
	if err != nil || stored.Role != syncenrollment.RoleIssuer || stored.OfferHash != acceptance.OfferHash {
		return syncstate.Device{}, ErrEnrollmentConflict
	}
	if !acceptance.ExpiresAt.Equal(stored.ExpiresAt) {
		return syncstate.Device{}, ErrEnrollmentConflict
	}
	identity, err := syncidentity.New(s.keys, database)
	if err != nil {
		return syncstate.Device{}, err
	}
	local, err := identity.Ensure(ctx, s.Record.ID)
	if err != nil {
		return syncstate.Device{}, err
	}
	clear(local.PrivateKey)
	if acceptance.SourceDeviceID != local.Device.DeviceID {
		return syncstate.Device{}, ErrEnrollmentConflict
	}
	now := time.Now().UTC()
	if stored.State == syncenrollment.StateCompleted {
		if stored.PeerDeviceID != acceptance.TargetDeviceID || stored.AcceptanceHash != acceptanceHash {
			return syncstate.Device{}, ErrEnrollmentConflict
		}
	} else {
		if err := enrollmentapp.RequireActiveAcceptance(acceptance, now); err != nil {
			return syncstate.Device{}, err
		}
	}
	target, err := ensureSyncDevice(ctx, database, s.Record.ID, acceptance.TargetDeviceID, acceptance.TargetPublicKey, acceptance.AcceptedAt, "sync-enrollment-target:"+acceptance.EnrollmentID)
	if err != nil {
		return syncstate.Device{}, err
	}
	if _, _, err := database.CompleteEnrollment(ctx, enrollmentstore.CompleteInput{EnrollmentID: acceptance.EnrollmentID, VaultID: s.Record.ID, TargetDeviceID: acceptance.TargetDeviceID, OfferHash: acceptance.OfferHash, AcceptanceHash: acceptanceHash, OccurredAt: now}); err != nil {
		return syncstate.Device{}, err
	}
	return target, nil
}

func (s *Session) ImportAndApplySyncPack(ctx context.Context, input ImportSyncPackInput) (syncpack.ApplyImportResult, error) {
	database, err := s.enrollmentDatabase()
	if err != nil || s.keys == nil || ctx == nil {
		return syncpack.ApplyImportResult{}, ErrNotOpen
	}
	vaultKey, err := s.keys.Get(ctx, keyvault.Reference{VaultID: s.Record.ID, KeyID: VaultKeyID})
	if err != nil {
		return syncpack.ApplyImportResult{}, err
	}
	defer clear(vaultKey)
	importer, err := syncpack.NewImporter(syncpack.New(), database)
	if err != nil {
		return syncpack.ApplyImportResult{}, err
	}
	return importer.Apply(ctx, syncpack.PrepareImportInput{Encoded: input.Encoded, VaultID: s.Record.ID, DeviceID: input.DeviceID, EncryptionKey: vaultKey, ReceivedAt: input.ReceivedAt})
}

func (s *Session) enrollmentDatabase() (EnrollmentDatabase, error) {
	if s == nil || s.database == nil {
		return nil, ErrNotOpen
	}
	database, ok := s.database.(EnrollmentDatabase)
	if !ok {
		return nil, ErrEnrollmentUnsupported
	}
	return database, nil
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

func (c *Creator) AcceptEnrollmentOffer(ctx context.Context, input AcceptEnrollmentOfferInput) (AcceptedEnrollment, error) {
	if c == nil || c.keys == nil || c.databases == nil || c.catalog == nil || ctx == nil {
		return AcceptedEnrollment{}, ErrInvalidInput
	}
	codec := enrollmentapp.NewCodec()
	offer, offerHash, err := codec.OpenOffer(ctx, input.Encoded, input.Secret)
	if err != nil {
		return AcceptedEnrollment{}, err
	}
	defer clear(offer.VaultKey)

	entry, cataloged, err := c.catalogEntry(ctx, offer.VaultID)
	if err != nil {
		return AcceptedEnrollment{}, err
	}
	if cataloged {
		if entry.State != vaultcatalog.StateActive {
			return AcceptedEnrollment{}, ErrEnrollmentConflict
		}
		session, err := c.Open(ctx, offer.VaultID)
		if err != nil {
			return AcceptedEnrollment{}, err
		}
		database, err := session.enrollmentDatabase()
		if err != nil {
			_ = session.Close()
			return AcceptedEnrollment{}, err
		}
		stored, response, err := database.GetEnrollment(ctx, offer.VaultID, offer.EnrollmentID)
		if err != nil || stored.Role != syncenrollment.RoleRecipient || stored.State != syncenrollment.StateAccepted || stored.OfferHash != offerHash || stored.PeerDeviceID != offer.SourceDeviceID {
			clear(response)
			_ = session.Close()
			return AcceptedEnrollment{}, ErrEnrollmentConflict
		}
		identity, err := syncidentity.New(c.keys, database)
		if err != nil {
			clear(response)
			_ = session.Close()
			return AcceptedEnrollment{}, err
		}
		local, err := identity.Ensure(ctx, offer.VaultID)
		if err != nil {
			clear(response)
			_ = session.Close()
			return AcceptedEnrollment{}, err
		}
		clear(local.PrivateKey)
		if err := validateStoredAcceptance(ctx, codec, response, input.Secret, offer, offerHash, stored.AcceptanceHash, local.Device.DeviceID); err != nil {
			clear(response)
			_ = session.Close()
			return AcceptedEnrollment{}, err
		}
		return AcceptedEnrollment{Session: session, EnrollmentID: offer.EnrollmentID, EncodedAcceptance: response, SourceDeviceID: offer.SourceDeviceID, TargetDeviceID: local.Device.DeviceID, Replay: true}, nil
	}
	if err := enrollmentapp.RequireActiveOffer(offer, c.now().UTC()); err != nil {
		return AcceptedEnrollment{}, err
	}
	if err := c.ensureEnrollmentVaultKey(ctx, offer.VaultID, offer.VaultKey); err != nil {
		return AcceptedEnrollment{}, err
	}
	database, err := c.databases.Create(ctx, offer.VaultID, VaultKeyID, offer.VaultKey)
	if errors.Is(err, vaultdb.ErrAlreadyExists) {
		database, err = c.databases.Open(ctx, offer.VaultID, VaultKeyID, offer.VaultKey)
	}
	if err != nil {
		return AcceptedEnrollment{}, fmt.Errorf("open enrollment Vault database: %w", err)
	}
	keepOpen := false
	defer func() {
		if !keepOpen {
			_ = database.Close()
		}
	}()
	record, err := database.GetVault(ctx, offer.VaultID)
	if errors.Is(err, vaultstore.ErrNotFound) {
		record, err = database.CreateVault(ctx, vaultstore.CreateInput{VaultID: offer.VaultID, RetentionDays: offer.RetentionDays, OccurredAt: offer.VaultCreatedAt, IdempotencyKey: "vault-enrollment-bootstrap:" + offer.EnrollmentID})
	}
	if err != nil {
		return AcceptedEnrollment{}, fmt.Errorf("initialize enrollment Vault state: %w", err)
	}
	if record.ID != offer.VaultID || record.Status != vault.StatusActive || record.RetentionDays != offer.RetentionDays || !record.CreatedAt.Equal(offer.VaultCreatedAt) {
		return AcceptedEnrollment{}, ErrEnrollmentConflict
	}
	enrollmentDatabase, ok := database.(EnrollmentDatabase)
	if !ok {
		return AcceptedEnrollment{}, ErrEnrollmentUnsupported
	}
	if _, err := ensureSyncDevice(ctx, enrollmentDatabase, offer.VaultID, offer.SourceDeviceID, offer.SourcePublicKey, offer.IssuedAt, "sync-enrollment-source:"+offer.EnrollmentID); err != nil {
		return AcceptedEnrollment{}, err
	}
	identity, err := syncidentity.New(c.keys, enrollmentDatabase)
	if err != nil {
		return AcceptedEnrollment{}, err
	}
	local, err := identity.Ensure(ctx, offer.VaultID)
	if err != nil {
		return AcceptedEnrollment{}, err
	}
	defer clear(local.PrivateKey)
	if stored, response, getErr := enrollmentDatabase.GetEnrollment(ctx, offer.VaultID, offer.EnrollmentID); getErr == nil {
		if stored.Role != syncenrollment.RoleRecipient || stored.State != syncenrollment.StateAccepted || stored.OfferHash != offerHash || stored.PeerDeviceID != offer.SourceDeviceID {
			clear(response)
			return AcceptedEnrollment{}, ErrEnrollmentConflict
		}
		if err := validateStoredAcceptance(ctx, codec, response, input.Secret, offer, offerHash, stored.AcceptanceHash, local.Device.DeviceID); err != nil {
			clear(response)
			return AcceptedEnrollment{}, err
		}
		if err := c.ensureCatalogEntry(ctx, vaultcatalog.Entry{VaultID: offer.VaultID, CreatedAt: record.CreatedAt, State: vaultcatalog.StateActive}); err != nil {
			clear(response)
			return AcceptedEnrollment{}, err
		}
		keepOpen = true
		return AcceptedEnrollment{Session: &Session{Record: record, database: database, keys: c.keys}, EnrollmentID: offer.EnrollmentID, EncodedAcceptance: response, SourceDeviceID: offer.SourceDeviceID, TargetDeviceID: local.Device.DeviceID, Replay: true}, nil
	} else if !errors.Is(getErr, enrollmentstore.ErrNotFound) {
		return AcceptedEnrollment{}, getErr
	}
	acceptedAt := c.now().UTC()
	created, err := codec.CreateAcceptance(ctx, enrollmentapp.CreateAcceptanceInput{Offer: offer, OfferHash: offerHash, TargetDeviceID: local.Device.DeviceID, TargetPublicKey: local.Device.PublicKey, SigningKey: local.PrivateKey, AcceptedAt: acceptedAt, Secret: input.Secret})
	if err != nil {
		return AcceptedEnrollment{}, err
	}
	_, storedResponse, replay, err := enrollmentDatabase.RecordEnrollmentAcceptance(ctx, enrollmentstore.RecordAcceptanceInput{EnrollmentID: offer.EnrollmentID, VaultID: offer.VaultID, SourceDeviceID: offer.SourceDeviceID, OfferHash: offerHash, AcceptanceHash: created.Hash, EncodedAcceptance: created.Encoded, ExpiresAt: offer.ExpiresAt, OccurredAt: acceptedAt})
	if err != nil {
		return AcceptedEnrollment{}, err
	}
	if err := c.ensureCatalogEntry(ctx, vaultcatalog.Entry{VaultID: offer.VaultID, CreatedAt: record.CreatedAt, State: vaultcatalog.StateActive}); err != nil {
		clear(storedResponse)
		return AcceptedEnrollment{}, err
	}
	keepOpen = true
	return AcceptedEnrollment{Session: &Session{Record: record, database: database, keys: c.keys}, EnrollmentID: offer.EnrollmentID, EncodedAcceptance: storedResponse, SourceDeviceID: offer.SourceDeviceID, TargetDeviceID: local.Device.DeviceID, Replay: replay}, nil
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

func (c *Creator) ensureEnrollmentVaultKey(ctx context.Context, vaultID string, expected []byte) error {
	if len(expected) != vaultKeyBytes {
		return ErrEnrollmentConflict
	}
	ref := keyvault.Reference{VaultID: vaultID, KeyID: VaultKeyID}
	err := c.keys.Put(ctx, ref, expected)
	if err == nil {
		return nil
	}
	if !errors.Is(err, keyvault.ErrAlreadyExists) {
		return fmt.Errorf("persist enrolled Vault key: %w", err)
	}
	stored, err := c.keys.Get(ctx, ref)
	if err != nil {
		return fmt.Errorf("load existing enrolled Vault key: %w", err)
	}
	defer clear(stored)
	if !bytes.Equal(stored, expected) {
		return ErrEnrollmentConflict
	}
	return nil
}

func (c *Creator) catalogEntry(ctx context.Context, vaultID string) (vaultcatalog.Entry, bool, error) {
	entries, err := c.catalog.List(ctx)
	if err != nil {
		return vaultcatalog.Entry{}, false, fmt.Errorf("read protected Vault catalog: %w", err)
	}
	for _, entry := range entries {
		if entry.VaultID == vaultID {
			return entry, true, nil
		}
	}
	return vaultcatalog.Entry{}, false, nil
}

func (c *Creator) ensureCatalogEntry(ctx context.Context, expected vaultcatalog.Entry) error {
	if err := c.catalog.Add(ctx, expected); err == nil {
		return nil
	} else if !errors.Is(err, vaultcatalog.ErrConflict) {
		return fmt.Errorf("register enrolled Vault in catalog: %w", err)
	}
	stored, found, err := c.catalogEntry(ctx, expected.VaultID)
	if err != nil {
		return err
	}
	if !found || stored.State != vaultcatalog.StateActive || !stored.CreatedAt.Equal(expected.CreatedAt) {
		return ErrEnrollmentConflict
	}
	return nil
}

func ensureSyncDevice(ctx context.Context, store syncstore.Store, vaultID, deviceID string, publicKey ed25519.PublicKey, occurredAt time.Time, idempotencyKey string) (syncstate.Device, error) {
	stored, err := store.GetSyncDevice(ctx, vaultID, deviceID)
	if errors.Is(err, syncstore.ErrDeviceNotFound) {
		stored, err = store.RegisterSyncDevice(ctx, syncstore.RegisterDeviceInput{VaultID: vaultID, DeviceID: deviceID, PublicKey: publicKey, OccurredAt: occurredAt, IdempotencyKey: idempotencyKey})
		if errors.Is(err, syncstore.ErrDeviceExists) {
			stored, err = store.GetSyncDevice(ctx, vaultID, deviceID)
		}
	}
	if err != nil {
		return syncstate.Device{}, err
	}
	if stored.State != syncstate.DeviceActive || stored.DeviceID != deviceID || !bytes.Equal(stored.PublicKey, publicKey) {
		return syncstate.Device{}, ErrEnrollmentConflict
	}
	return stored, nil
}

func validateStoredAcceptance(ctx context.Context, codec *enrollmentapp.Codec, encoded []byte, secret string, offer syncenrollment.Offer, offerHash, expectedAcceptanceHash, targetDeviceID string) error {
	acceptance, acceptanceHash, err := codec.OpenAcceptance(ctx, encoded, secret)
	if err != nil {
		return err
	}
	if acceptanceHash != expectedAcceptanceHash || acceptance.EnrollmentID != offer.EnrollmentID || acceptance.VaultID != offer.VaultID || acceptance.OfferHash != offerHash || acceptance.SourceDeviceID != offer.SourceDeviceID || acceptance.TargetDeviceID != targetDeviceID || !acceptance.ExpiresAt.Equal(offer.ExpiresAt) {
		return ErrEnrollmentConflict
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
