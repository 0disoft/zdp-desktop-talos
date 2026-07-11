package artifact

import (
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
)

func TestRecordRequiresCanonicalSHA256Hashes(t *testing.T) {
	record := Record{
		ID: "artifact", VaultID: "vault", SchemaVersion: 1, Sensitivity: event.SensitivityPrivate,
		ContentType: "text/plain", SizeBytes: 1, ContentHash: strings.Repeat("a", 64),
		CiphertextHash: strings.Repeat("b", 64), CreatedAt: time.Now().UTC(),
	}
	if err := record.Validate(); err != nil {
		t.Fatal(err)
	}
	record.ContentHash = strings.Repeat("A", 64)
	if err := record.Validate(); err == nil {
		t.Fatal("uppercase hash was accepted")
	}
}
