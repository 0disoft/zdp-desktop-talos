package folderexchange

import (
	"bytes"
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncexchange"
)

func TestFolderExchangePublishesIdempotentlyAndReadsInSequenceOrder(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	root := filepath.Join(t.TempDir(), "exchange")
	exchange := New()
	createdAt := time.Date(2026, 7, 18, 19, 0, 0, 0, time.UTC)
	descriptor2 := syncexchange.PackDescriptor{VaultID: "vault", DeviceID: "device", PackID: "pack-2", SequenceStart: 3, SequenceEnd: 4, CreatedAt: createdAt}
	descriptor1 := syncexchange.PackDescriptor{VaultID: "vault", DeviceID: "device", PackID: "pack-1", SequenceStart: 1, SequenceEnd: 2, CreatedAt: createdAt}

	second, replay, err := exchange.WritePack(ctx, root, descriptor2, []byte("pack-two"))
	if err != nil || replay || filepath.IsAbs(second.RelativePath) {
		t.Fatalf("second=%+v replay=%v error=%v", second, replay, err)
	}
	first, replay, err := exchange.WritePack(ctx, root, descriptor1, []byte("pack-one"))
	if err != nil || replay || first.SequenceStart != 1 {
		t.Fatalf("first=%+v replay=%v error=%v", first, replay, err)
	}
	replayed, replay, err := exchange.WritePack(ctx, root, descriptor1, []byte("pack-one"))
	if err != nil || !replay || replayed != first {
		t.Fatalf("replayed=%+v replay=%v error=%v", replayed, replay, err)
	}
	if _, _, err := exchange.WritePack(ctx, root, descriptor1, []byte("changed")); !errors.Is(err, syncexchange.ErrPackConflict) {
		t.Fatalf("changed bytes error=%v", err)
	}

	read, err := exchange.ReadPacks(ctx, root, descriptor1.VaultID, descriptor1.DeviceID)
	if err != nil {
		t.Fatal(err)
	}
	defer clearReadPacks(read)
	if len(read) != 2 || read[0].SequenceStart != 1 || read[1].SequenceStart != 3 || !bytes.Equal(read[0].Encoded, []byte("pack-one")) || !bytes.Equal(read[1].Encoded, []byte("pack-two")) {
		t.Fatalf("read=%+v", read)
	}
}

func TestFolderExchangeRejectsMalformedPackEntryAndUnsafeRoot(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	exchange := New()
	root := t.TempDir()
	descriptor := syncexchange.PackDescriptor{VaultID: "vault", DeviceID: "device", PackID: "pack", SequenceStart: 1, SequenceEnd: 1, CreatedAt: time.Date(2026, 7, 18, 19, 0, 0, 0, time.UTC)}
	stored, _, err := exchange.WritePack(ctx, root, descriptor, []byte("pack"))
	if err != nil {
		t.Fatal(err)
	}
	directory := filepath.Dir(filepath.Join(root, filepath.FromSlash(stored.RelativePath)))
	if err := os.WriteFile(filepath.Join(directory, "malformed.talos-pack"), []byte("bad"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := exchange.ReadPacks(ctx, root, descriptor.VaultID, descriptor.DeviceID); !errors.Is(err, syncexchange.ErrUnsafePath) {
		t.Fatalf("malformed pack error=%v", err)
	}
	unsafeRoot := filepath.Join(t.TempDir(), "not-a-directory")
	if err := os.WriteFile(unsafeRoot, []byte("file"), 0o600); err != nil {
		t.Fatal(err)
	}
	if _, _, err := exchange.WritePack(ctx, unsafeRoot, descriptor, []byte("pack")); !errors.Is(err, syncexchange.ErrUnsafePath) {
		t.Fatalf("unsafe root error=%v", err)
	}
}

func TestFolderExchangeEnforcesPackAndAggregateBounds(t *testing.T) {
	t.Parallel()
	exchange := New()
	descriptor := syncexchange.PackDescriptor{VaultID: "vault", DeviceID: "device", PackID: "pack", SequenceStart: 1, SequenceEnd: 1, CreatedAt: time.Now().UTC()}
	if _, _, err := exchange.WritePack(context.Background(), t.TempDir(), descriptor, bytes.Repeat([]byte("x"), syncexchange.MaxPackBytes+1)); !errors.Is(err, syncexchange.ErrInvalidRequest) {
		t.Fatalf("oversized write error=%v", err)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := exchange.WritePack(canceled, t.TempDir(), descriptor, []byte("pack")); !errors.Is(err, context.Canceled) {
		t.Fatalf("canceled write error=%v", err)
	}
	if strings.Contains(hashSegment("vault", "vault"), string(filepath.Separator)) {
		t.Fatal("hashed path segment contains a separator")
	}
}
