//go:build windows

package doctor

import (
	"context"
	"testing"
)

func TestKeyStoreCheck(t *testing.T) {
	t.Parallel()
	check, err := keyStoreCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if check.Name != "key_store" || check.Status != "passed" {
		t.Fatalf("check = %+v", check)
	}
	if check.Details["provider"] != "windows-dpapi" || check.Details["scope"] != "current_user" {
		t.Fatalf("details = %+v", check.Details)
	}
}

func TestSQLiteCheckIncludesVaultRecoveryAndIdempotency(t *testing.T) {
	t.Parallel()
	check, err := sqliteCheck(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if check.Name != "encrypted_sqlite" || check.Status != "passed" {
		t.Fatalf("check = %+v", check)
	}
	if check.Details["idempotency_bound"] != true || check.Details["vault_state_restart"] != true {
		t.Fatalf("details = %+v", check.Details)
	}
	if check.Details["vault_state_revision"] != 2 {
		t.Fatalf("details = %+v", check.Details)
	}
}
