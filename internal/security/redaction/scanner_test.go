package redaction

import (
	"context"
	"strings"
	"testing"
)

func TestScannerRedactsCredentialShapesAndPrivateKeys(t *testing.T) {
	t.Parallel()
	input := "+token=ghp_abcdefghijklmnopqrstuvwxyz123456\n+url=https://user:password@example.test/path\n+json={\"password\":\"json-secret-marker\"}\n+-----BEGIN PRIVATE KEY-----\n+private-material\n+-----END PRIVATE KEY-----\n context"
	result, err := NewScanner().Redact(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"ghp_abcdefghijklmnopqrstuvwxyz123456", "password@example", "json-secret-marker", "PRIVATE KEY", "private-material"} {
		if strings.Contains(result.Text, secret) {
			t.Fatalf("secret %q survived: %q", secret, result.Text)
		}
	}
	if result.Findings < 6 || !strings.Contains(result.Text, `"password":"[REDACTED]"`) || !strings.Contains(result.Text, " context") {
		t.Fatalf("result=%+v", result)
	}
}

func TestScannerRedactsHighConfidenceProviderTokenPrefixes(t *testing.T) {
	t.Parallel()

	credentials := []string{
		"glpat-" + strings.Repeat("a", 24),
		"gldt-" + strings.Repeat("b", 24),
		"xoxb-" + strings.Repeat("1-", 10) + "abc",
		"xapp-1-" + strings.Repeat("c", 20) + "-" + strings.Repeat("d", 20),
		"AIza" + strings.Repeat("E", 35),
		"sk_live_" + strings.Repeat("f", 24),
		"rk_test_" + strings.Repeat("g", 24),
	}
	for _, credential := range credentials {
		result, err := NewScanner().Redact(context.Background(), "value="+credential)
		if err != nil {
			t.Fatal(err)
		}
		if result.Findings != 1 || strings.Contains(result.Text, credential) || result.Text != "value="+replacement {
			t.Fatalf("credential shape was not redacted: findings=%d text=%q", result.Findings, result.Text)
		}
	}
}

func TestScannerDoesNotRedactPublicOrIncompleteProviderIdentifiers(t *testing.T) {
	t.Parallel()

	input := strings.Join([]string{
		"glpat-short",
		"xoxb-short",
		"AIza" + strings.Repeat("A", 34),
		"pk_live_" + strings.Repeat("h", 24),
	}, "\n")
	result, err := NewScanner().Redact(context.Background(), input)
	if err != nil {
		t.Fatal(err)
	}
	if result.Findings != 0 || result.Text != input {
		t.Fatalf("non-secret identifiers changed: %+v", result)
	}
}
