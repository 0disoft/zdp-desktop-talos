package vaultbootstrap

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/artifact"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/decision"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/execution"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/permission"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/task"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultdb"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestCreatePersistsKeyBeforeInitializingVault(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{}
	database := &fakeDatabase{}
	databases := &fakeDatabaseFactory{database: database}
	catalog := &fakeCatalog{}
	creator, err := NewCreator(keys, databases, catalog)
	if err != nil {
		t.Fatal(err)
	}
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	session, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30})
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !keys.present || !databases.created || database.input.VaultID != session.Record.ID {
		t.Fatalf("incomplete creation: keys=%+v databases=%+v input=%+v", keys, databases, database.input)
	}
	if len(catalog.entries) != 1 || catalog.entries[0].VaultID != session.Record.ID {
		t.Fatalf("Vault was not cataloged: %+v", catalog.entries)
	}
	if database.input.IdempotencyKey != "vault-bootstrap:"+session.Record.ID || session.Record.Revision != 1 {
		t.Fatalf("unexpected session: %+v, input=%+v", session.Record, database.input)
	}
	if len(databases.keySnapshot) != vaultKeyBytes || bytes.Equal(databases.keySnapshot, make([]byte, vaultKeyBytes)) {
		t.Fatal("database factory did not receive generated key bytes")
	}
}

func TestCreateRemovesKeyWhenDatabaseCreationFails(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{}
	databases := &fakeDatabaseFactory{createErr: errors.New("disk full")}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{})
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	if _, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30}); err == nil || errors.Is(err, ErrCompensationFailed) {
		t.Fatalf("unexpected error: %v", err)
	}
	if keys.present || !keys.deleted || !databases.removed {
		t.Fatalf("partial creation was not compensated: keys=%+v databases=%+v", keys, databases)
	}
}

func TestCreateRemovesDatabaseAndKeyWhenStateInitializationFails(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{}
	database := &fakeDatabase{createErr: errors.New("transaction failed")}
	databases := &fakeDatabaseFactory{database: database}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{})
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	if _, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30}); err == nil {
		t.Fatal("state initialization failure was hidden")
	}
	if keys.present || !keys.deleted || !databases.removed || !database.closed {
		t.Fatalf("partial creation was not compensated: keys=%+v databases=%+v database=%+v", keys, databases, database)
	}
}

func TestCreateReportsIncompleteCompensation(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{deleteErr: errors.New("access denied")}
	databases := &fakeDatabaseFactory{createErr: errors.New("disk full"), removeErr: errors.New("file busy")}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{})
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	_, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30})
	if !errors.Is(err, ErrCompensationFailed) || !errors.Is(err, keys.deleteErr) || !errors.Is(err, databases.removeErr) {
		t.Fatalf("compensation error lost evidence: %v", err)
	}
}

func TestCreateCompensatesCatalogDatabaseAndKeyWhenCatalogWriteFails(t *testing.T) {
	t.Parallel()
	keys := &fakeKeyStore{}
	database := &fakeDatabase{}
	databases := &fakeDatabaseFactory{database: database}
	catalog := &fakeCatalog{addErr: errors.New("catalog write failed")}
	creator, _ := NewCreator(keys, databases, catalog)
	creator.now = func() time.Time { return time.Unix(1_800_000_000, 0).UTC() }
	creator.random = bytes.NewReader(testRandomBytes())

	if _, err := creator.Create(context.Background(), CreateInput{RetentionDays: 30}); err == nil {
		t.Fatal("catalog failure was hidden")
	}
	if keys.present || !database.closed || !databases.removed || catalog.removed.VaultID == "" {
		t.Fatalf("catalog failure was not compensated: keys=%+v db=%+v factory=%+v catalog=%+v", keys, database, databases, catalog)
	}
}

func TestOpenRequiresCatalogAndLoadsMatchingVault(t *testing.T) {
	t.Parallel()
	createdAt := time.Unix(1_800_000_000, 0).UTC()
	vaultID := "00000000-0000-7000-8000-000000000001"
	keys := &fakeKeyStore{value: bytes.Repeat([]byte{3}, vaultKeyBytes), present: true}
	database := &fakeDatabase{stored: vault.Record{
		ID: vaultID, Revision: 2, Status: vault.StatusActive, RetentionDays: 90,
		CreatedAt: createdAt, UpdatedAt: createdAt.Add(time.Hour), LastEventID: "event-2",
	}}
	databases := &fakeDatabaseFactory{database: database}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{entries: []vaultcatalog.Entry{{VaultID: vaultID, CreatedAt: createdAt}}})

	if _, err := creator.Open(context.Background(), "unknown"); !errors.Is(err, ErrNotCataloged) {
		t.Fatalf("unknown Vault error = %v", err)
	}
	session, err := creator.Open(context.Background(), vaultID)
	if err != nil {
		t.Fatal(err)
	}
	defer session.Close()
	if !databases.opened || session.Record != database.stored {
		t.Fatalf("session=%+v database=%+v factory=%+v", session.Record, database.stored, databases)
	}
}

func TestOpenRejectsUnknownAndMismatchedVault(t *testing.T) {
	t.Parallel()
	createdAt := time.Unix(1_800_000_000, 0).UTC()
	keys := &fakeKeyStore{value: bytes.Repeat([]byte{9}, vaultKeyBytes), present: true}
	database := &fakeDatabase{stored: vault.Record{
		ID: "00000000-0000-7000-8000-000000000002", Revision: 1, Status: vault.StatusActive, RetentionDays: 30,
		CreatedAt: createdAt, UpdatedAt: createdAt, LastEventID: "event-1",
	}}
	databases := &fakeDatabaseFactory{database: database}
	creator, _ := NewCreator(keys, databases, &fakeCatalog{entries: []vaultcatalog.Entry{{VaultID: "00000000-0000-7000-8000-000000000001", CreatedAt: createdAt}}})

	if _, err := creator.Open(context.Background(), "00000000-0000-7000-8000-000000000003"); !errors.Is(err, ErrNotCataloged) || databases.opened {
		t.Fatalf("unknown open error=%v factory=%+v", err, databases)
	}
	if _, err := creator.Open(context.Background(), "00000000-0000-7000-8000-000000000001"); !errors.Is(err, ErrNotCataloged) || !database.closed {
		t.Fatalf("mismatched open error=%v database=%+v", err, database)
	}
}

func TestSessionUpdatesRetentionWithRevisionAndIdempotency(t *testing.T) {
	t.Parallel()
	database := &fakeDatabase{stored: vault.Record{
		ID: "00000000-0000-7000-8000-000000000001", Revision: 2, Status: vault.StatusActive,
		RetentionDays: 30, CreatedAt: time.Unix(1_800_000_000, 0).UTC(), UpdatedAt: time.Unix(1_800_000_000, 0).UTC(), LastEventID: "event-2",
	}}
	session := &Session{Record: database.stored, database: database}
	record, err := session.UpdateRetention(context.Background(), UpdateRetentionInput{
		ExpectedRevision: 2, RetentionDays: 90, IdempotencyKey: "retention-request-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if record.Revision != 3 || record.RetentionDays != 90 || session.Record != record {
		t.Fatalf("record=%+v session=%+v", record, session.Record)
	}
	if database.updateInput.ExpectedRevision != 2 || database.updateInput.IdempotencyKey != "retention-request-1" {
		t.Fatalf("input=%+v", database.updateInput)
	}
	if _, err := session.UpdateRetention(context.Background(), UpdateRetentionInput{ExpectedRevision: 2, RetentionDays: 365, IdempotencyKey: "stale"}); !errors.Is(err, vaultstore.ErrRevisionConflict) {
		t.Fatalf("stale revision error=%v", err)
	}
}

func TestHardPurgeRequiresExactConfirmationAndCompletesAllStores(t *testing.T) {
	t.Parallel()
	createdAt := time.Unix(1_800_000_000, 0).UTC()
	vaultID := "00000000-0000-7000-8000-000000000001"
	keys := &fakeKeyStore{present: true, value: bytes.Repeat([]byte{4}, vaultKeyBytes)}
	database := &fakeDatabase{stored: vault.Record{ID: vaultID, Revision: 3, Status: vault.StatusActive, RetentionDays: 30, CreatedAt: createdAt, UpdatedAt: createdAt, LastEventID: "event-3"}}
	databases := &fakeDatabaseFactory{database: database}
	catalog := &fakeCatalog{entries: []vaultcatalog.Entry{{VaultID: vaultID, CreatedAt: createdAt, State: vaultcatalog.StateActive}}}
	creator, _ := NewCreator(keys, databases, catalog)
	session := &Session{Record: database.stored, database: database}

	if err := creator.HardPurge(context.Background(), session, HardPurgeInput{ExpectedRevision: 2, Confirmation: vaultID}); !errors.Is(err, vaultstore.ErrRevisionConflict) {
		t.Fatalf("stale purge error=%v", err)
	}
	if err := creator.HardPurge(context.Background(), session, HardPurgeInput{ExpectedRevision: 3, Confirmation: "wrong"}); !errors.Is(err, ErrInvalidInput) {
		t.Fatalf("confirmation error=%v", err)
	}
	if database.closed || keys.deleted || databases.purged {
		t.Fatal("invalid purge changed durable state")
	}
	if err := creator.HardPurge(context.Background(), session, HardPurgeInput{ExpectedRevision: 3, Confirmation: vaultID}); err != nil {
		t.Fatal(err)
	}
	if !database.closed || !keys.deleted || !databases.purged || len(catalog.entries) != 0 {
		t.Fatalf("database=%+v keys=%+v factory=%+v catalog=%+v", database, keys, databases, catalog)
	}
}

func TestHardPurgeResumesAfterKeyDeletionFailure(t *testing.T) {
	t.Parallel()
	createdAt := time.Unix(1_800_000_000, 0).UTC()
	vaultID := "00000000-0000-7000-8000-000000000001"
	keys := &fakeKeyStore{present: true, value: bytes.Repeat([]byte{5}, vaultKeyBytes), deleteErr: errors.New("key locked")}
	database := &fakeDatabase{stored: vault.Record{ID: vaultID, Revision: 1, Status: vault.StatusActive, RetentionDays: 30, CreatedAt: createdAt, UpdatedAt: createdAt, LastEventID: "event-1"}}
	databases := &fakeDatabaseFactory{database: database}
	catalog := &fakeCatalog{entries: []vaultcatalog.Entry{{VaultID: vaultID, CreatedAt: createdAt, State: vaultcatalog.StateActive}}}
	creator, _ := NewCreator(keys, databases, catalog)
	session := &Session{Record: database.stored, database: database}

	err := creator.HardPurge(context.Background(), session, HardPurgeInput{ExpectedRevision: 1, Confirmation: vaultID})
	if !errors.Is(err, ErrPurgeIncomplete) || catalog.entries[0].State != vaultcatalog.StatePurgePending || databases.purged {
		t.Fatalf("first purge err=%v catalog=%+v factory=%+v", err, catalog, databases)
	}
	keys.deleteErr = nil
	if err := creator.ReconcilePurges(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !keys.deleted || !databases.purged || len(catalog.entries) != 0 {
		t.Fatalf("resume keys=%+v factory=%+v catalog=%+v", keys, databases, catalog)
	}
}

func TestHardPurgeResumesAfterCiphertextOrJournalCleanupFailure(t *testing.T) {
	t.Parallel()
	for _, testCase := range []struct {
		name         string
		purgeErr     error
		catalogErr   error
		clearFailure func(*fakeDatabaseFactory, *fakeCatalog)
	}{
		{name: "ciphertext", purgeErr: errors.New("file locked"), clearFailure: func(factory *fakeDatabaseFactory, _ *fakeCatalog) { factory.purgeErr = nil }},
		{name: "journal", catalogErr: errors.New("catalog locked"), clearFailure: func(_ *fakeDatabaseFactory, catalog *fakeCatalog) { catalog.removeErr = nil }},
	} {
		testCase := testCase
		t.Run(testCase.name, func(t *testing.T) {
			t.Parallel()
			createdAt := time.Unix(1_800_000_000, 0).UTC()
			vaultID := "00000000-0000-7000-8000-000000000001"
			keys := &fakeKeyStore{present: true, value: bytes.Repeat([]byte{6}, vaultKeyBytes)}
			database := &fakeDatabase{stored: vault.Record{ID: vaultID, Revision: 1, Status: vault.StatusActive, RetentionDays: 30, CreatedAt: createdAt, UpdatedAt: createdAt, LastEventID: "event-1"}}
			databases := &fakeDatabaseFactory{database: database, purgeErr: testCase.purgeErr}
			catalog := &fakeCatalog{entries: []vaultcatalog.Entry{{VaultID: vaultID, CreatedAt: createdAt, State: vaultcatalog.StateActive}}, removeErr: testCase.catalogErr}
			creator, _ := NewCreator(keys, databases, catalog)
			session := &Session{Record: database.stored, database: database}
			err := creator.HardPurge(context.Background(), session, HardPurgeInput{ExpectedRevision: 1, Confirmation: vaultID})
			if !errors.Is(err, ErrPurgeIncomplete) || keys.present || len(catalog.entries) != 1 {
				t.Fatalf("first purge err=%v keys=%+v catalog=%+v", err, keys, catalog)
			}
			testCase.clearFailure(databases, catalog)
			if err := creator.ReconcilePurges(context.Background()); err != nil {
				t.Fatal(err)
			}
			if len(catalog.entries) != 0 {
				t.Fatalf("catalog after recovery=%+v", catalog.entries)
			}
		})
	}
}

type fakeKeyStore struct {
	present   bool
	deleted   bool
	deleteErr error
	value     []byte
}

func testRandomBytes() []byte {
	data := make([]byte, 48)
	data[16] = 1
	return data
}

func (s *fakeKeyStore) Put(_ context.Context, _ keyvault.Reference, value []byte) error {
	s.present = true
	s.value = append([]byte(nil), value...)
	return nil
}
func (s *fakeKeyStore) Get(context.Context, keyvault.Reference) ([]byte, error) {
	if !s.present {
		return nil, keyvault.ErrNotFound
	}
	return append([]byte(nil), s.value...), nil
}
func (s *fakeKeyStore) Rotate(context.Context, keyvault.Reference, []byte) error { return nil }
func (s *fakeKeyStore) Delete(context.Context, keyvault.Reference) error {
	s.deleted = true
	if s.deleteErr == nil {
		s.present = false
	}
	return s.deleteErr
}

type fakeDatabaseFactory struct {
	database    vaultdb.Database
	createErr   error
	removeErr   error
	created     bool
	opened      bool
	removed     bool
	purged      bool
	purgeErr    error
	keySnapshot []byte
}

func (f *fakeDatabaseFactory) Create(_ context.Context, _ string, _ string, key []byte) (vaultdb.Database, error) {
	f.created = true
	f.keySnapshot = append([]byte(nil), key...)
	return f.database, f.createErr
}
func (f *fakeDatabaseFactory) Open(context.Context, string, string, []byte) (vaultdb.Database, error) {
	f.opened = true
	return f.database, nil
}
func (f *fakeDatabaseFactory) Remove(context.Context, string) error {
	f.removed = true
	return f.removeErr
}
func (f *fakeDatabaseFactory) Purge(context.Context, string) error {
	f.purged = true
	return f.purgeErr
}

type fakeDatabase struct {
	input       vaultstore.CreateInput
	createErr   error
	closed      bool
	stored      vault.Record
	updateInput vaultstore.UpdateRetentionInput
}

func (d *fakeDatabase) CreateVault(_ context.Context, input vaultstore.CreateInput) (vault.Record, error) {
	d.input = input
	if d.createErr != nil {
		return vault.Record{}, d.createErr
	}
	return vault.Record{ID: input.VaultID, Revision: 1, Status: vault.StatusActive, RetentionDays: input.RetentionDays, CreatedAt: input.OccurredAt, UpdatedAt: input.OccurredAt, LastEventID: "event-1"}, nil
}
func (d *fakeDatabase) GetVault(context.Context, string) (vault.Record, error) {
	return d.stored, nil
}
func (d *fakeDatabase) UpdateVaultRetention(_ context.Context, input vaultstore.UpdateRetentionInput) (vault.Record, error) {
	d.updateInput = input
	d.stored.Revision++
	d.stored.RetentionDays = input.RetentionDays
	d.stored.UpdatedAt = input.OccurredAt
	d.stored.LastEventID = "event-updated"
	return d.stored, nil
}
func (*fakeDatabase) PutArtifact(context.Context, artifactstore.PutInput) (artifact.Record, error) {
	return artifact.Record{}, artifactstore.ErrNotFound
}
func (*fakeDatabase) GetArtifact(context.Context, string) (artifact.Record, []byte, error) {
	return artifact.Record{}, nil, artifactstore.ErrNotFound
}
func (*fakeDatabase) ReconcileArtifacts(context.Context) error { return nil }
func (*fakeDatabase) CreateTaskContract(context.Context, taskstore.CreateInput) (taskstore.Created, error) {
	return taskstore.Created{}, taskstore.ErrNotFound
}
func (*fakeDatabase) ReviseTaskContract(context.Context, taskstore.ReviseInput) (taskstore.Created, error) {
	return taskstore.Created{}, taskstore.ErrNotFound
}
func (*fakeDatabase) CreateDecision(context.Context, decisionstore.CreateInput) (decisionstore.Result, error) {
	return decisionstore.Result{}, decisionstore.ErrNotFound
}
func (*fakeDatabase) GetDecision(context.Context, string) (decisionstore.Result, error) {
	return decisionstore.Result{Decision: decision.Record{}}, decisionstore.ErrNotFound
}
func (*fakeDatabase) ListDecisions(context.Context, string, string, int) ([]decisionstore.Result, error) {
	return nil, nil
}
func (*fakeDatabase) AnswerDecision(context.Context, decisionstore.AnswerInput) (decisionstore.Result, error) {
	return decisionstore.Result{}, decisionstore.ErrNotFound
}
func (*fakeDatabase) SupersedeDecision(context.Context, decisionstore.SupersedeInput) (decisionstore.Result, error) {
	return decisionstore.Result{}, decisionstore.ErrNotFound
}
func (*fakeDatabase) ResolveDecisionConflict(context.Context, decisionstore.ResolveInput) (decisionstore.Result, error) {
	return decisionstore.Result{}, decisionstore.ErrNotFound
}
func (*fakeDatabase) GetTask(context.Context, string) (task.Record, error) {
	return task.Record{}, taskstore.ErrNotFound
}
func (*fakeDatabase) GetTaskContract(context.Context, string, int) (task.ContractRevision, error) {
	return task.ContractRevision{}, taskstore.ErrNotFound
}
func (*fakeDatabase) SavePermissionGrant(context.Context, executionstore.SaveGrantInput) (permission.Grant, error) {
	return permission.Grant{}, executionstore.ErrNotFound
}
func (*fakeDatabase) CreatePermissionRequest(context.Context, executionstore.CreatePermissionRequestInput) (permission.Request, error) {
	return permission.Request{}, executionstore.ErrNotFound
}
func (*fakeDatabase) ListOpenPermissionRequests(context.Context, string, string, int) ([]permission.Request, error) {
	return nil, executionstore.ErrNotFound
}
func (*fakeDatabase) ResolvePermissionRequest(context.Context, executionstore.ResolvePermissionRequestInput) (executionstore.PermissionResolution, error) {
	return executionstore.PermissionResolution{}, executionstore.ErrNotFound
}
func (*fakeDatabase) ListActivePermissionGrants(context.Context, string, string) ([]permission.Grant, error) {
	return nil, nil
}
func (*fakeDatabase) PrepareAttempt(context.Context, executionstore.PrepareAttemptInput) (executionstore.Prepared, error) {
	return executionstore.Prepared{}, executionstore.ErrNotFound
}
func (*fakeDatabase) FinishAttempt(context.Context, executionstore.FinishAttemptInput) (execution.Attempt, error) {
	return execution.Attempt{}, executionstore.ErrNotFound
}
func (*fakeDatabase) FinishRun(context.Context, executionstore.FinishRunInput) (execution.Run, error) {
	return execution.Run{}, executionstore.ErrNotFound
}
func (*fakeDatabase) ReconcilePendingAttempts(context.Context, string, time.Time) (int, error) {
	return 0, nil
}
func (d *fakeDatabase) Close() error { d.closed = true; return nil }

type fakeCatalog struct {
	entries   []vaultcatalog.Entry
	addErr    error
	removed   vaultcatalog.Entry
	removeErr error
}

func (c *fakeCatalog) List(context.Context) ([]vaultcatalog.Entry, error) {
	return append([]vaultcatalog.Entry(nil), c.entries...), nil
}
func (c *fakeCatalog) PendingPurges(context.Context) ([]vaultcatalog.Entry, error) {
	var pending []vaultcatalog.Entry
	for _, entry := range c.entries {
		if entry.State == vaultcatalog.StatePurgePending {
			pending = append(pending, entry)
		}
	}
	return pending, nil
}
func (c *fakeCatalog) Add(_ context.Context, entry vaultcatalog.Entry) error {
	if c.addErr != nil {
		return c.addErr
	}
	c.entries = append(c.entries, entry)
	return nil
}
func (c *fakeCatalog) MarkPurgePending(_ context.Context, target vaultcatalog.Entry) error {
	for index := range c.entries {
		if c.entries[index].VaultID == target.VaultID {
			c.entries[index].State = vaultcatalog.StatePurgePending
			return nil
		}
	}
	return vaultcatalog.ErrNotFound
}
func (c *fakeCatalog) Remove(_ context.Context, entry vaultcatalog.Entry) error {
	c.removed = entry
	if c.removeErr != nil {
		return c.removeErr
	}
	filtered := c.entries[:0]
	for _, current := range c.entries {
		if current.VaultID != entry.VaultID || !current.CreatedAt.Equal(entry.CreatedAt) {
			filtered = append(filtered, current)
		}
	}
	c.entries = filtered
	return nil
}
