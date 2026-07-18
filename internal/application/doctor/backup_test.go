package doctor

import (
	"context"
	"testing"
)

func TestBackupCheckProvesEncryptedIsolatedPreflight(t *testing.T) {
	t.Parallel()
	check, err := backupCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if check.Name != "vault_backup" || check.Status != "passed" {
		t.Fatalf("check = %+v", check)
	}
	if check.Details["online_snapshot"] != true || check.Details["plaintext_marker_absent"] != true || check.Details["isolated_preflight"] != true || check.Details["live_vault_unchanged"] != true || check.Details["journaled_live_restore"] != true {
		t.Fatalf("details = %+v", check.Details)
	}
	if check.Details["artifact_count"] != 1 {
		t.Fatalf("details = %+v", check.Details)
	}
	if check.Details["restored_revision"] != 1 {
		t.Fatalf("details = %+v", check.Details)
	}
}
