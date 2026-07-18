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
