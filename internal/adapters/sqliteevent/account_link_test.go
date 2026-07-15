package sqliteevent

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/accountlink"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/accountstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
)

func TestAccountLinkLifecycleIsRevisionedEncryptedAndRestartSafe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	databasePath := filepath.Join(t.TempDir(), "vault.db")
	store := openTestStore(t, databasePath)
	now := time.Date(2026, 7, 15, 5, 0, 0, 0, time.UTC)
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-account", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault-account-create"}); err != nil {
		t.Fatal(err)
	}
	identity := accountlink.VerifiedIdentity{SubjectRef: "usr_private_marker_01", WorkspaceRef: "wks_private_marker_01", ConsentReceiptRef: "consent_private_marker_01", VerifiedAt: now.Add(time.Second)}
	input := accountstore.LinkInput{VaultID: "vault-account", ExpectedRevision: 0, Identity: identity, OccurredAt: now.Add(2 * time.Second), IdempotencyKey: "account-link-1"}
	linked, err := store.LinkAccount(ctx, input)
	if err != nil {
		t.Fatal(err)
	}
	if linked.State != accountlink.StateLinked || linked.Revision != 1 || linked.SubjectRef != identity.SubjectRef || linked.MembershipID == "" {
		t.Fatalf("linked=%+v", linked)
	}
	replayed, err := store.LinkAccount(ctx, input)
	if err != nil || replayed != linked {
		t.Fatalf("replayed=%+v error=%v", replayed, err)
	}
	if _, err := store.LinkAccount(ctx, accountstore.LinkInput{VaultID: input.VaultID, ExpectedRevision: 0, Identity: identity, IdempotencyKey: "account-link-stale"}); !errors.Is(err, accountstore.ErrRevisionConflict) {
		t.Fatalf("stale link error=%v", err)
	}
	unlinked, err := store.UnlinkAccount(ctx, accountstore.UnlinkInput{VaultID: input.VaultID, ExpectedRevision: 1, OccurredAt: now.Add(3 * time.Second), IdempotencyKey: "account-unlink-2"})
	if err != nil {
		t.Fatal(err)
	}
	if unlinked.State != accountlink.StateUnlinked || unlinked.Revision != 2 || unlinked.MembershipID != linked.MembershipID || unlinked.SubjectRef != "" || unlinked.ConsentReceiptRef != "" {
		t.Fatalf("unlinked=%+v", unlinked)
	}
	relinked, err := store.LinkAccount(ctx, accountstore.LinkInput{VaultID: input.VaultID, ExpectedRevision: 2, Identity: accountlink.VerifiedIdentity{SubjectRef: "usr_second_private_marker", ConsentReceiptRef: "consent_second_private_marker", VerifiedAt: now.Add(4 * time.Second)}, OccurredAt: now.Add(5 * time.Second), IdempotencyKey: "account-relink-3"})
	if err != nil || relinked.Revision != 3 || relinked.MembershipID != linked.MembershipID {
		t.Fatalf("relinked=%+v error=%v", relinked, err)
	}
	if err := store.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	if err := store.Close(); err != nil {
		t.Fatal(err)
	}
	contents, err := os.ReadFile(databasePath)
	if err != nil {
		t.Fatal(err)
	}
	for _, marker := range [][]byte{[]byte(identity.SubjectRef), []byte(identity.WorkspaceRef), []byte(identity.ConsentReceiptRef), []byte(relinked.SubjectRef), []byte(relinked.ConsentReceiptRef)} {
		if bytes.Contains(contents, marker) {
			t.Fatalf("SQLite database contains plaintext account reference %q", marker)
		}
	}
	reopened := openTestStore(t, databasePath)
	defer reopened.Close()
	restored, err := reopened.GetAccountLink(ctx, input.VaultID)
	if err != nil || restored != relinked {
		t.Fatalf("restored=%+v expected=%+v error=%v", restored, relinked, err)
	}
}

func TestAccountLinkIdempotencyRejectsChangedVerifiedIdentity(t *testing.T) {
	t.Parallel()
	store := openTestStore(t, filepath.Join(t.TempDir(), "vault.db"))
	defer store.Close()
	ctx := context.Background()
	now := time.Date(2026, 7, 15, 6, 0, 0, 0, time.UTC)
	if _, err := store.CreateVault(ctx, vaultstore.CreateInput{VaultID: "vault-account", RetentionDays: 30, OccurredAt: now, IdempotencyKey: "vault-account-create"}); err != nil {
		t.Fatal(err)
	}
	input := accountstore.LinkInput{VaultID: "vault-account", Identity: accountlink.VerifiedIdentity{SubjectRef: "usr_one", ConsentReceiptRef: "consent_one", VerifiedAt: now}, IdempotencyKey: "shared-account-command"}
	if _, err := store.LinkAccount(ctx, input); err != nil {
		t.Fatal(err)
	}
	input.Identity.SubjectRef = "usr_two"
	if _, err := store.LinkAccount(ctx, input); !errors.Is(err, accountstore.ErrIdempotencyConflict) {
		t.Fatalf("idempotency error=%v", err)
	}
}
