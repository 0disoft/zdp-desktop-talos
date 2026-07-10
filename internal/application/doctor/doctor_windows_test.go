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
