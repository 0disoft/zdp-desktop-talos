package doctor

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/dpapikeyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
	"github.com/0disoft/zdp-desktop-talos/internal/workeripc"
)

type Check struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Details map[string]any `json:"details,omitempty"`
}

type Report struct {
	Schema             string   `json:"schema"`
	Ready              bool     `json:"ready"`
	ProductionReady    bool     `json:"production_ready"`
	ProductionBlockers []string `json:"production_blockers,omitempty"`
	Checks             []Check  `json:"checks"`
}

func Run(ctx context.Context, workerPath string) Report {
	report := Report{
		Schema:             "talos.doctor/1",
		Ready:              true,
		ProductionReady:    false,
		ProductionBlockers: []string{"signed distribution and native release checks are not verified by this local doctor"},
	}
	report.Checks = append(report.Checks,
		Check{Name: "runtime", Status: "passed", Details: map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH}},
		gitCheck(),
	)

	keyStoreCheck, err := keyStoreCheck(ctx)
	if err != nil {
		if errors.Is(err, keyvault.ErrUnsupported) {
			report.ProductionBlockers = append(report.ProductionBlockers, "the current platform has no supported OS key-store adapter")
			report.Checks = append(report.Checks, Check{Name: "key_store", Status: "unsupported", Details: map[string]any{"reason": err.Error()}})
		} else {
			report.Ready = false
			report.ProductionBlockers = append(report.ProductionBlockers, "the OS key-store self-test failed")
			report.Checks = append(report.Checks, Check{Name: "key_store", Status: "failed", Details: map[string]any{"error": err.Error()}})
		}
	} else {
		report.Checks = append(report.Checks, keyStoreCheck)
	}

	if check, err := sqliteCheck(ctx); err != nil {
		report.Ready = false
		report.Checks = append(report.Checks, Check{Name: "encrypted_sqlite", Status: "failed", Details: map[string]any{"error": err.Error()}})
	} else {
		report.Checks = append(report.Checks, check)
	}

	if check, err := backupCheck(ctx); err != nil {
		report.Ready = false
		report.ProductionBlockers = append(report.ProductionBlockers, "the encrypted Vault backup self-test failed")
		report.Checks = append(report.Checks, Check{Name: "vault_backup", Status: "failed", Details: map[string]any{"error": err.Error()}})
	} else {
		report.Checks = append(report.Checks, check)
	}

	if workerPath == "" {
		workerPath = defaultWorkerPath()
	}
	if _, err := os.Stat(workerPath); err != nil {
		report.Ready = false
		report.Checks = append(report.Checks, Check{Name: "worker_handshake", Status: "unavailable", Details: map[string]any{"reason": "sibling talos-worker binary not found"}})
	} else {
		handshake, err := workeripc.Probe(ctx, workerPath)
		if err != nil {
			report.Ready = false
			report.Checks = append(report.Checks, Check{Name: "worker_handshake", Status: "failed", Details: map[string]any{"error": err.Error()}})
		} else {
			report.Checks = append(report.Checks, Check{Name: "worker_handshake", Status: "passed", Details: map[string]any{"protocol": handshake.ProtocolVersion, "worker": handshake.WorkerVersion, "capabilities": handshake.Capabilities}})
		}
	}
	report.ProductionReady = report.Ready && len(report.ProductionBlockers) == 0
	return report
}

func gitCheck() Check {
	path, err := exec.LookPath("git")
	if err != nil {
		return Check{Name: "git", Status: "unavailable"}
	}
	return Check{Name: "git", Status: "passed", Details: map[string]any{"available": true, "executable": filepath.Base(path)}}
}

func keyStoreCheck(ctx context.Context) (Check, error) {
	directory, err := os.MkdirTemp("", "talos-key-store-doctor-")
	if err != nil {
		return Check{}, fmt.Errorf("create key-store test directory: %w", err)
	}
	defer os.RemoveAll(directory)

	store, err := dpapikeyvault.Open(directory)
	if err != nil {
		return Check{}, err
	}
	ref := keyvault.Reference{VaultID: "doctor-vault", KeyID: "vault-kek"}
	first := []byte("talos-doctor-dpapi-marker-first")
	second := []byte("talos-doctor-dpapi-marker-next!")
	if err := store.Put(ctx, ref, first); err != nil {
		return Check{}, fmt.Errorf("store DPAPI test key: %w", err)
	}
	got, err := store.Get(ctx, ref)
	if err != nil {
		return Check{}, fmt.Errorf("load DPAPI test key: %w", err)
	}
	if !bytes.Equal(got, first) {
		return Check{}, fmt.Errorf("DPAPI key store returned different initial key bytes")
	}
	if err := store.Rotate(ctx, ref, second); err != nil {
		return Check{}, fmt.Errorf("rotate DPAPI test key: %w", err)
	}
	got, err = store.Get(ctx, ref)
	if err != nil {
		return Check{}, fmt.Errorf("load rotated DPAPI test key: %w", err)
	}
	if !bytes.Equal(got, second) {
		return Check{}, fmt.Errorf("DPAPI key store returned different rotated key bytes")
	}

	entries, err := os.ReadDir(directory)
	if err != nil {
		return Check{}, fmt.Errorf("inspect DPAPI test directory: %w", err)
	}
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		stored, err := os.ReadFile(filepath.Join(directory, entry.Name()))
		if err != nil {
			return Check{}, fmt.Errorf("inspect DPAPI record: %w", err)
		}
		if bytes.Contains(stored, first) || bytes.Contains(stored, second) {
			return Check{}, fmt.Errorf("plaintext marker found in DPAPI record")
		}
	}
	if err := store.Delete(ctx, ref); err != nil {
		return Check{}, fmt.Errorf("delete DPAPI test key: %w", err)
	}

	return Check{
		Name:   "key_store",
		Status: "passed",
		Details: map[string]any{
			"provider":                "windows-dpapi",
			"scope":                   "current_user",
			"roundtrip":               true,
			"rotation":                true,
			"plaintext_marker_absent": true,
		},
	}, nil
}

func sqliteCheck(ctx context.Context) (Check, error) {
	directory, err := os.MkdirTemp("", "talos-doctor-")
	if err != nil {
		return Check{}, fmt.Errorf("create temporary directory: %w", err)
	}
	defer os.RemoveAll(directory)

	key := make([]byte, 32)
	if _, err := rand.Read(key); err != nil {
		return Check{}, fmt.Errorf("generate temporary key: %w", err)
	}
	sealer, err := envelope.NewSealer("doctor-key", key)
	if err != nil {
		return Check{}, err
	}
	databasePath := filepath.Join(directory, "doctor.db")
	store, err := sqliteevent.Open(databasePath, sealer)
	if err != nil {
		return Check{}, err
	}
	marker := []byte("talos-doctor-private-marker")
	record, err := store.Append(ctx, eventstore.AppendInput{
		VaultID:        "doctor-vault",
		Type:           "doctor.roundtrip",
		SchemaVersion:  1,
		Sensitivity:    event.SensitivityPrivate,
		Payload:        marker,
		OccurredAt:     time.Now().UTC(),
		IdempotencyKey: "doctor-roundtrip",
	})
	if err != nil {
		_ = store.Close()
		return Check{}, err
	}
	conflictingInput := eventstore.AppendInput{
		VaultID:        "doctor-vault",
		Type:           "doctor.roundtrip",
		SchemaVersion:  1,
		Sensitivity:    event.SensitivityPrivate,
		Payload:        []byte("different-doctor-payload"),
		OccurredAt:     record.OccurredAt,
		IdempotencyKey: "doctor-roundtrip",
	}
	if _, err := store.Append(ctx, conflictingInput); !errors.Is(err, sqliteevent.ErrIdempotencyConflict) {
		_ = store.Close()
		return Check{}, fmt.Errorf("idempotency conflict self-test returned %v", err)
	}
	vaultCreated, err := store.CreateVault(ctx, vaultstore.CreateInput{
		VaultID:        "doctor-vault",
		RetentionDays:  30,
		IdempotencyKey: "doctor-vault-create",
	})
	if err != nil {
		_ = store.Close()
		return Check{}, fmt.Errorf("create materialized Vault state: %w", err)
	}
	vaultUpdated, err := store.UpdateVaultRetention(ctx, vaultstore.UpdateRetentionInput{
		VaultID:          vaultCreated.ID,
		ExpectedRevision: vaultCreated.Revision,
		RetentionDays:    60,
		IdempotencyKey:   "doctor-vault-retention",
	})
	if err != nil {
		_ = store.Close()
		return Check{}, fmt.Errorf("update materialized Vault state: %w", err)
	}
	artifactMarker := []byte("talos-doctor-artifact-private-marker")
	storedArtifact, err := store.PutArtifact(ctx, artifactstore.PutInput{
		VaultID: "doctor-vault", SchemaVersion: 1, Sensitivity: event.SensitivitySensitive,
		ContentType: "text/plain; charset=utf-8", Payload: artifactMarker,
	})
	if err != nil {
		_ = store.Close()
		return Check{}, fmt.Errorf("store encrypted artifact: %w", err)
	}
	if err := store.Checkpoint(ctx); err != nil {
		_ = store.Close()
		return Check{}, err
	}
	if err := store.Close(); err != nil {
		return Check{}, err
	}
	databaseBytes, err := os.ReadFile(databasePath)
	if err != nil {
		return Check{}, err
	}
	if bytes.Contains(databaseBytes, marker) {
		return Check{}, fmt.Errorf("plaintext marker found in SQLite database")
	}
	artifactFiles, err := os.ReadDir(databasePath + ".blobs")
	if err != nil {
		return Check{}, fmt.Errorf("inspect encrypted artifact files: %w", err)
	}
	if len(artifactFiles) != 1 {
		return Check{}, fmt.Errorf("inspect encrypted artifact files: count=%d", len(artifactFiles))
	}
	artifactCiphertext, err := os.ReadFile(filepath.Join(databasePath+".blobs", artifactFiles[0].Name()))
	if err != nil {
		return Check{}, err
	}
	if bytes.Contains(artifactCiphertext, artifactMarker) {
		return Check{}, fmt.Errorf("plaintext marker found in artifact ciphertext")
	}

	reopened, err := sqliteevent.Open(databasePath, sealer)
	if err != nil {
		return Check{}, err
	}
	defer reopened.Close()
	restored, err := reopened.Get(ctx, record.ID)
	if err != nil {
		return Check{}, err
	}
	if !bytes.Equal(restored.Payload, marker) {
		return Check{}, fmt.Errorf("restarted store returned different payload")
	}
	restoredVault, err := reopened.GetVault(ctx, vaultCreated.ID)
	if err != nil {
		return Check{}, fmt.Errorf("restore materialized Vault state: %w", err)
	}
	if restoredVault != vaultUpdated {
		return Check{}, fmt.Errorf("restarted store returned different Vault state")
	}
	restoredArtifact, restoredArtifactPayload, err := reopened.GetArtifact(ctx, storedArtifact.ID)
	if err != nil {
		return Check{}, fmt.Errorf("restore encrypted artifact: %w", err)
	}
	defer clear(restoredArtifactPayload)
	if restoredArtifact != storedArtifact || !bytes.Equal(restoredArtifactPayload, artifactMarker) {
		return Check{}, fmt.Errorf("restarted store returned different artifact")
	}
	return Check{Name: "encrypted_sqlite", Status: "passed", Details: map[string]any{
		"restart_roundtrip":         true,
		"plaintext_marker_absent":   true,
		"idempotency_bound":         true,
		"vault_state_revision":      restoredVault.Revision,
		"vault_state_restart":       true,
		"artifact_restart":          true,
		"artifact_plaintext_absent": true,
	}}, nil
}

func defaultWorkerPath() string {
	executable, err := os.Executable()
	if err != nil {
		return ""
	}
	name := "talos-worker"
	if runtime.GOOS == "windows" {
		name += ".exe"
	}
	return filepath.Join(filepath.Dir(executable), name)
}
