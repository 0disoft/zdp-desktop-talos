package wailsapi

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/artifact"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/execution"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/memory"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/patchaction"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/verification"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestVaultServiceCreatesThenLocksSession(t *testing.T) {
	t.Parallel()
	keys := &serviceKeyStore{}
	database := &serviceDatabase{}
	creator, err := vaultbootstrap.NewCreator(keys, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	if err != nil {
		t.Fatal(err)
	}
	service := NewVaultService(creator, nil)

	created := service.Create(30, "correlation-create")
	if created.Error != nil || created.Vault == nil || created.Vault.State != "unlocked" || created.Vault.Revision != 1 {
		t.Fatalf("create result = %+v", created)
	}
	duplicate := service.Create(30, "correlation-duplicate")
	if duplicate.Error == nil || duplicate.Error.Code != "VAULT_ALREADY_OPEN" {
		t.Fatalf("duplicate result = %+v", duplicate)
	}
	locked := service.Lock("correlation-lock")
	if locked.Error != nil || locked.Vault == nil || locked.Vault.State != "locked" || !database.closed {
		t.Fatalf("lock result = %+v, database=%+v", locked, database)
	}
}

func TestVaultServiceListsAndReopensCreatedVault(t *testing.T) {
	t.Parallel()
	keys := &serviceKeyStore{}
	database := &serviceDatabase{}
	factory := &serviceDatabaseFactory{database: database}
	catalog := &serviceCatalog{}
	creator, _ := vaultbootstrap.NewCreator(keys, factory, catalog)
	service := NewVaultService(creator, nil)
	created := service.Create(90, "create")
	if created.Error != nil || created.Vault == nil {
		t.Fatalf("create = %+v", created)
	}
	vaultID := created.Vault.VaultID
	if result := service.Lock("lock"); result.Error != nil {
		t.Fatalf("lock = %+v", result)
	}
	listed := service.List("list")
	if listed.Error != nil || len(listed.Vaults) != 1 || listed.Vaults[0].VaultID != vaultID {
		t.Fatalf("list = %+v", listed)
	}
	reopened := service.Open(vaultID, "open")
	if reopened.Error != nil || reopened.Vault == nil || reopened.Vault.State != "unlocked" || reopened.Vault.RetentionDays != 90 {
		t.Fatalf("open = %+v", reopened)
	}
}

func TestVaultServiceUpdatesRetentionAndRejectsStaleRevision(t *testing.T) {
	t.Parallel()
	database := &serviceDatabase{}
	creator, _ := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	service := NewVaultService(creator, nil)
	created := service.Create(30, "create")
	updated := service.UpdateRetention(90, created.Vault.Revision, "request-1", "update")
	if updated.Error != nil || updated.Vault == nil || updated.Vault.Revision != 2 || updated.Vault.RetentionDays != 90 {
		t.Fatalf("updated=%+v", updated)
	}
	stale := service.UpdateRetention(365, 1, "request-2", "stale")
	if stale.Error == nil || stale.Error.Code != "VAULT_REVISION_CONFLICT" {
		t.Fatalf("stale=%+v", stale)
	}
	if status := service.Status(); status.Revision != 2 || status.RetentionDays != 90 {
		t.Fatalf("status=%+v", status)
	}
}

func TestVaultServiceHardPurgeRequiresExactVaultID(t *testing.T) {
	t.Parallel()
	database := &serviceDatabase{}
	creator, _ := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	service := NewVaultService(creator, nil)
	created := service.Create(30, "create")
	invalid := service.HardPurge(created.Vault.Revision, "wrong", "invalid-purge")
	if invalid.Error == nil || invalid.Error.Code != "VAULT_INPUT_INVALID" || service.Status().State != "unlocked" {
		t.Fatalf("invalid=%+v status=%+v", invalid, service.Status())
	}
	purged := service.HardPurge(created.Vault.Revision, created.Vault.VaultID, "purge")
	if purged.Error != nil || purged.Vault == nil || purged.Vault.State != "locked" || service.Status().State != "locked" {
		t.Fatalf("purged=%+v status=%+v", purged, service.Status())
	}
}

func TestVaultServiceFailsClosedWhenStorageIsUnavailable(t *testing.T) {
	t.Parallel()
	service := NewVaultService(nil, errors.New("C:\\Users\\private\\keys"))
	result := service.Create(30, "correlation-unavailable")
	if result.Error == nil || result.Error.Code != "VAULT_STORAGE_UNAVAILABLE" {
		t.Fatalf("result = %+v", result)
	}
	if result.Error.Message == "" || result.Error.CorrelationID != "correlation-unavailable" {
		t.Fatalf("error = %+v", result.Error)
	}
}

func TestVaultServiceDoesNotReportOpenAfterCloseFailure(t *testing.T) {
	t.Parallel()
	database := &serviceDatabase{closeErr: errors.New("flush failed")}
	creator, _ := vaultbootstrap.NewCreator(&serviceKeyStore{}, &serviceDatabaseFactory{database: database}, &serviceCatalog{})
	service := NewVaultService(creator, nil)
	if result := service.Create(30, "create"); result.Error != nil {
		t.Fatalf("create result = %+v", result)
	}
	result := service.Lock("lock")
	if result.Error == nil || result.Error.Code != "VAULT_LOCK_FAILED" {
		t.Fatalf("lock result = %+v", result)
	}
	if status := service.Status(); status.State != "locked" {
		t.Fatalf("status after close failure = %+v", status)
	}
}

func TestMapErrorPrioritizesIncompleteCleanup(t *testing.T) {
	t.Parallel()
	mapped := MapError(errors.Join(vaultbootstrap.ErrCompensationFailed, vaultbootstrap.ErrInvalidInput), "cleanup")
	if mapped.Code != "VAULT_CLEANUP_INCOMPLETE" {
		t.Fatalf("mapped = %+v", mapped)
	}
}

func TestMapErrorExposesPendingPurgeWithoutInternalPaths(t *testing.T) {
	t.Parallel()
	mapped := MapError(errors.Join(vaultbootstrap.ErrPurgeIncomplete, errors.New("C:\\Users\\private\\vault.db")), "purge")
	if mapped.Code != "VAULT_PURGE_INCOMPLETE" || mapped.CorrelationID != "purge" {
		t.Fatalf("mapped=%+v", mapped)
	}
	if mapped.Message == "" || mapped.Message == "C:\\Users\\private\\vault.db" {
		t.Fatalf("unsafe message=%q", mapped.Message)
	}
}

type serviceKeyStore struct {
	value   []byte
	present bool
}

func (s *serviceKeyStore) Put(_ context.Context, _ keyvault.Reference, value []byte) error {
	s.value = append([]byte(nil), value...)
	s.present = true
	return nil
}
func (s *serviceKeyStore) Get(context.Context, keyvault.Reference) ([]byte, error) {
	if !s.present {
		return nil, keyvault.ErrNotFound
	}
	return append([]byte(nil), s.value...), nil
}
func (*serviceKeyStore) Rotate(context.Context, keyvault.Reference, []byte) error { return nil }
func (s *serviceKeyStore) Delete(context.Context, keyvault.Reference) error {
	s.present = false
	s.value = nil
	return nil
}

type serviceDatabaseFactory struct{ database vaultdb.Database }

func (f *serviceDatabaseFactory) Create(context.Context, string, string, []byte) (vaultdb.Database, error) {
	return f.database, nil
}
func (f *serviceDatabaseFactory) Open(context.Context, string, string, []byte) (vaultdb.Database, error) {
	if database, ok := f.database.(*serviceDatabase); ok {
		database.closed = false
	}
	return f.database, nil
}
func (*serviceDatabaseFactory) Remove(context.Context, string) error { return nil }
func (*serviceDatabaseFactory) Purge(context.Context, string) error  { return nil }

type serviceDatabase struct {
	closed             bool
	closeErr           error
	record             vault.Record
	taskInput          taskstore.CreateInput
	reviseInput        taskstore.ReviseInput
	decisionInput      decisionstore.CreateInput
	answerInput        decisionstore.AnswerInput
	supersedeInput     decisionstore.SupersedeInput
	resolveInput       decisionstore.ResolveInput
	decisionResult     decisionstore.Result
	decisionList       []decisionstore.Result
	taskCreated        taskstore.Created
	taskErr            error
	memoryInputs       []memorystore.CreateCandidateInput
	memoryRecords      map[string]memory.Record
	memoryKeys         map[string]string
	accountRecord      accountlink.Record
	accountUnlinkInput accountstore.UnlinkInput
}

func (d *serviceDatabase) CreateVault(_ context.Context, input vaultstore.CreateInput) (vault.Record, error) {
	d.record = vault.Record{
		ID: input.VaultID, Revision: 1, Status: vault.StatusActive, RetentionDays: input.RetentionDays,
		CreatedAt: input.OccurredAt, UpdatedAt: input.OccurredAt, LastEventID: "event-1",
	}
	return d.record, nil
}
func (d *serviceDatabase) GetVault(context.Context, string) (vault.Record, error) {
	return d.record, nil
}
func (d *serviceDatabase) UpdateVaultRetention(_ context.Context, input vaultstore.UpdateRetentionInput) (vault.Record, error) {
	if input.ExpectedRevision != d.record.Revision {
		return vault.Record{}, vaultstore.ErrRevisionConflict
	}
	d.record.Revision++
	d.record.RetentionDays = input.RetentionDays
	d.record.UpdatedAt = input.OccurredAt
	d.record.LastEventID = "event-retention"
	return d.record, nil
}
func (*serviceDatabase) PutArtifact(context.Context, artifactstore.PutInput) (artifact.Record, error) {
	return artifact.Record{}, artifactstore.ErrNotFound
}
func (*serviceDatabase) GetArtifact(context.Context, string) (artifact.Record, []byte, error) {
	return artifact.Record{}, nil, artifactstore.ErrNotFound
}
func (*serviceDatabase) ReconcileArtifacts(context.Context) error { return nil }
func (d *serviceDatabase) CreateTaskContract(_ context.Context, input taskstore.CreateInput) (taskstore.Created, error) {
	d.taskInput = input
	if d.taskCreated.Task.ID != "" {
		d.taskCreated.Task.VaultID = input.VaultID
		d.taskCreated.Task.WorkspaceRoot = input.WorkspaceRoot
		d.taskCreated.Task.BaselineCommit = input.BaselineCommit
		d.taskCreated.Contract.BaselineCommit = input.BaselineCommit
	}
	return d.taskCreated, d.taskErr
}
func (d *serviceDatabase) ReviseTaskContract(_ context.Context, input taskstore.ReviseInput) (taskstore.Created, error) {
	d.reviseInput = input
	return d.taskCreated, d.taskErr
}
func (d *serviceDatabase) CreateDecision(_ context.Context, input decisionstore.CreateInput) (decisionstore.Result, error) {
	d.decisionInput = input
	return d.decisionResult, nil
}
func (d *serviceDatabase) GetDecision(context.Context, string) (decisionstore.Result, error) {
	return d.decisionResult, nil
}
func (d *serviceDatabase) ListDecisions(context.Context, string, string, int) ([]decisionstore.Result, error) {
	return append([]decisionstore.Result(nil), d.decisionList...), nil
}
func (d *serviceDatabase) AnswerDecision(_ context.Context, input decisionstore.AnswerInput) (decisionstore.Result, error) {
	d.answerInput = input
	return d.decisionResult, nil
}
func (d *serviceDatabase) SupersedeDecision(_ context.Context, input decisionstore.SupersedeInput) (decisionstore.Result, error) {
	d.supersedeInput = input
	return d.decisionResult, nil
}
func (d *serviceDatabase) ResolveDecisionConflict(_ context.Context, input decisionstore.ResolveInput) (decisionstore.Result, error) {
	d.resolveInput = input
	return d.decisionResult, nil
}
func (d *serviceDatabase) GetTask(_ context.Context, taskID string) (task.Record, error) {
	if d.taskCreated.Task.ID == taskID {
		return d.taskCreated.Task, nil
	}
	return task.Record{}, taskstore.ErrNotFound
}
func (d *serviceDatabase) GetTaskContract(_ context.Context, taskID string, revision int) (task.ContractRevision, error) {
	if d.taskCreated.Contract.TaskID == taskID && d.taskCreated.Contract.Revision == revision {
		return d.taskCreated.Contract, nil
	}
	return task.ContractRevision{}, taskstore.ErrNotFound
}
func (d *serviceDatabase) CreateMemoryCandidate(_ context.Context, input memorystore.CreateCandidateInput) (memory.Record, error) {
	if d.memoryRecords == nil {
		d.memoryRecords = map[string]memory.Record{}
		d.memoryKeys = map[string]string{}
	}
	if memoryID, exists := d.memoryKeys[input.IdempotencyKey]; exists {
		return d.memoryRecords[memoryID], nil
	}
	d.memoryInputs = append(d.memoryInputs, input)
	memoryID := "memory-" + string(rune('0'+len(d.memoryInputs)))
	record := memory.Record{ID: memoryID, VaultID: input.VaultID, Kind: input.Kind, State: memory.StateCandidate, Scope: input.Scope, Statement: input.Statement, Rationale: input.Rationale, Applicability: input.Applicability, EvidenceEventIDs: append([]string(nil), input.EvidenceEventIDs...), SourceActor: input.SourceActor, Confidence: input.Confidence, Sensitivity: input.Sensitivity, Revision: 1, CreatedAt: input.OccurredAt, UpdatedAt: input.OccurredAt, CreatedEventID: "memory-created-event", LastEventID: "memory-created-event"}
	d.memoryRecords[memoryID] = record
	d.memoryKeys[input.IdempotencyKey] = memoryID
	return record, nil
}
func (d *serviceDatabase) TransitionMemory(_ context.Context, input memorystore.TransitionInput) (memory.Record, error) {
	record, exists := d.memoryRecords[input.MemoryID]
	if !exists || record.VaultID != input.VaultID {
		return memory.Record{}, memorystore.ErrNotFound
	}
	if record.Revision != input.ExpectedRevision {
		return memory.Record{}, memorystore.ErrRevisionConflict
	}
	if !memory.CanTransition(record.State, input.NextState) {
		return memory.Record{}, memorystore.ErrTransitionRejected
	}
	if input.NextState == memory.StateSuperseded {
		replacement, exists := d.memoryRecords[input.SupersededBy]
		if !exists || replacement.VaultID != input.VaultID || (replacement.State != memory.StateApproved && replacement.State != memory.StateStable) {
			return memory.Record{}, memorystore.ErrTransitionRejected
		}
		record.SupersededBy = input.SupersededBy
	}
	if input.NextState == memory.StateApproved {
		record.ExpiresAt = input.ExpiresAt
	} else if !input.ExpiresAt.IsZero() {
		record.ExpiresAt = input.ExpiresAt
	}
	if input.OccurredAt.IsZero() {
		input.OccurredAt = record.UpdatedAt.Add(time.Nanosecond)
	}
	record.State = input.NextState
	record.Revision++
	record.UpdatedAt = input.OccurredAt
	record.ReviewedAt = input.OccurredAt
	record.LastEventID = "memory-review-event"
	d.memoryRecords[input.MemoryID] = record
	return record, nil
}
func (d *serviceDatabase) GetMemory(_ context.Context, vaultID, memoryID string) (memory.Record, error) {
	record, exists := d.memoryRecords[memoryID]
	if !exists || record.VaultID != vaultID {
		return memory.Record{}, memorystore.ErrNotFound
	}
	return record, nil
}
func (d *serviceDatabase) ListMemoryCandidates(_ context.Context, vaultID string, limit int) ([]memory.Record, error) {
	result := make([]memory.Record, 0, limit)
	for _, record := range d.memoryRecords {
		if record.VaultID == vaultID && record.State == memory.StateCandidate && len(result) < limit {
			result = append(result, record)
		}
	}
	return result, nil
}
func (d *serviceDatabase) ListActiveMemories(_ context.Context, input memorystore.ListActiveInput) ([]memory.Record, error) {
	result := make([]memory.Record, 0, input.Limit)
	for _, record := range d.memoryRecords {
		if record.VaultID != input.VaultID || (record.State != memory.StateApproved && record.State != memory.StateStable) || len(result) >= input.Limit {
			continue
		}
		if record.Scope.Kind == memory.ScopeWorkspace && record.Scope.WorkspaceID != input.WorkspaceID {
			continue
		}
		if !input.At.IsZero() && record.IsExpired(input.At) {
			continue
		}
		result = append(result, record)
	}
	return result, nil
}
func (d *serviceDatabase) ListMemories(_ context.Context, input memorystore.ListInput) ([]memory.Record, error) {
	result := make([]memory.Record, 0, input.Limit)
	for _, record := range d.memoryRecords {
		if record.VaultID == input.VaultID && len(result) < input.Limit {
			result = append(result, record)
		}
	}
	return result, nil
}
func (*serviceDatabase) SavePermissionGrant(context.Context, executionstore.SaveGrantInput) (permission.Grant, error) {
	return permission.Grant{}, executionstore.ErrNotFound
}
func (*serviceDatabase) CreatePermissionRequest(context.Context, executionstore.CreatePermissionRequestInput) (permission.Request, error) {
	return permission.Request{}, executionstore.ErrNotFound
}
func (*serviceDatabase) ListOpenPermissionRequests(context.Context, string, string, int) ([]permission.Request, error) {
	return nil, executionstore.ErrNotFound
}
func (*serviceDatabase) ResolvePermissionRequest(context.Context, executionstore.ResolvePermissionRequestInput) (executionstore.PermissionResolution, error) {
	return executionstore.PermissionResolution{}, executionstore.ErrNotFound
}
func (*serviceDatabase) ListActivePermissionGrants(context.Context, string, string) ([]permission.Grant, error) {
	return nil, nil
}
func (*serviceDatabase) PrepareAttempt(context.Context, executionstore.PrepareAttemptInput) (executionstore.Prepared, error) {
	return executionstore.Prepared{}, executionstore.ErrNotFound
}
func (*serviceDatabase) FinishAttempt(context.Context, executionstore.FinishAttemptInput) (executionstore.FinishedAttempt, error) {
	return executionstore.FinishedAttempt{}, executionstore.ErrNotFound
}
func (*serviceDatabase) FinishRun(context.Context, executionstore.FinishRunInput) (execution.Run, error) {
	return execution.Run{}, executionstore.ErrNotFound
}
func (*serviceDatabase) ReconcilePendingAttempts(context.Context, string, time.Time) (int, error) {
	return 0, nil
}
func (*serviceDatabase) GetLatestVerificationEvidence(context.Context, string, string) (verification.Evidence, error) {
	return verification.Evidence{}, executionstore.ErrNotFound
}
func (*serviceDatabase) PreparePatchAction(context.Context, patchstore.PrepareInput) (patchstore.Prepared, error) {
	return patchstore.Prepared{}, patchstore.ErrNotFound
}
func (*serviceDatabase) FinishPatchAction(context.Context, patchstore.FinishInput) (patchaction.Record, error) {
	return patchaction.Record{}, patchstore.ErrNotFound
}
func (*serviceDatabase) LinkAccount(context.Context, accountstore.LinkInput) (accountlink.Record, error) {
	return accountlink.Record{}, accountstore.ErrNotFound
}
func (d *serviceDatabase) UnlinkAccount(_ context.Context, input accountstore.UnlinkInput) (accountlink.Record, error) {
	d.accountUnlinkInput = input
	if d.accountRecord.MembershipID == "" {
		return accountlink.Record{}, accountstore.ErrNotFound
	}
	if d.accountRecord.Revision != input.ExpectedRevision || d.accountRecord.State != accountlink.StateLinked {
		return accountlink.Record{}, accountstore.ErrRevisionConflict
	}
	d.accountRecord.State = accountlink.StateUnlinked
	d.accountRecord.Revision++
	d.accountRecord.LinkReceiptRef = ""
	d.accountRecord.SubjectRef = ""
	d.accountRecord.WorkspaceRef = ""
	d.accountRecord.ConsentReceiptRef = ""
	d.accountRecord.LinkedAt = time.Time{}
	d.accountRecord.LastVerifiedAt = time.Time{}
	d.accountRecord.UpdatedAt = d.accountRecord.UpdatedAt.Add(time.Nanosecond)
	d.accountRecord.LastEventID = "account-unlink-event"
	return d.accountRecord, nil
}
func (d *serviceDatabase) GetAccountLink(context.Context, string) (accountlink.Record, error) {
	if d.accountRecord.MembershipID == "" {
		return accountlink.Record{}, accountstore.ErrNotFound
	}
	return d.accountRecord, nil
}
func (*serviceDatabase) RegisterSyncDevice(context.Context, syncstore.RegisterDeviceInput) (syncstate.Device, error) {
	return syncstate.Device{}, syncstore.ErrDeviceNotFound
}
func (*serviceDatabase) RevokeSyncDevice(context.Context, syncstore.RevokeDeviceInput) (syncstate.Device, error) {
	return syncstate.Device{}, syncstore.ErrDeviceNotFound
}
func (*serviceDatabase) GetSyncDevice(context.Context, string, string) (syncstate.Device, error) {
	return syncstate.Device{}, syncstore.ErrDeviceNotFound
}
func (*serviceDatabase) ListSyncDevices(context.Context, string, int) ([]syncstate.Device, error) {
	return nil, nil
}
func (*serviceDatabase) RecordValidatedSyncPack(context.Context, syncstore.RecordPackInput) (syncstate.PackReceipt, bool, error) {
	return syncstate.PackReceipt{}, false, syncstore.ErrPackConflict
}
func (*serviceDatabase) GetValidatedSyncPack(context.Context, string, string) (syncstate.PackReceipt, []byte, error) {
	return syncstate.PackReceipt{}, nil, syncstore.ErrPackConflict
}
func (*serviceDatabase) PrepareSyncExport(context.Context, syncstore.PrepareExportInput) (syncstore.PreparedExport, bool, error) {
	return syncstore.PreparedExport{}, false, syncstore.ErrNoExportableEvents
}
func (*serviceDatabase) FinalizeSyncExport(context.Context, syncstore.FinalizeExportInput) (syncstate.ExportBatch, []byte, bool, error) {
	return syncstate.ExportBatch{}, nil, false, syncstore.ErrExportConflict
}
func (*serviceDatabase) GetSyncExport(context.Context, string, string) (syncstate.ExportBatch, []byte, error) {
	return syncstate.ExportBatch{}, nil, syncstore.ErrExportConflict
}
func (d *serviceDatabase) Close() error { d.closed = true; return d.closeErr }

type serviceCatalog struct{ entries []vaultcatalog.Entry }

func (c *serviceCatalog) List(context.Context) ([]vaultcatalog.Entry, error) { return c.entries, nil }
func (c *serviceCatalog) PendingPurges(context.Context) ([]vaultcatalog.Entry, error) {
	return nil, nil
}
func (c *serviceCatalog) Add(_ context.Context, entry vaultcatalog.Entry) error {
	c.entries = append(c.entries, entry)
	return nil
}
func (*serviceCatalog) MarkPurgePending(context.Context, vaultcatalog.Entry) error { return nil }
func (*serviceCatalog) Remove(context.Context, vaultcatalog.Entry) error           { return nil }
