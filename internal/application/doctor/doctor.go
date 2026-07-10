package doctor

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/eventstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
	"github.com/0disoft/zdp-desktop-talos/internal/workeripc"
)

type Check struct {
	Name    string         `json:"name"`
	Status  string         `json:"status"`
	Details map[string]any `json:"details,omitempty"`
}

type Report struct {
	Schema          string  `json:"schema"`
	Ready           bool    `json:"ready"`
	ProductionReady bool    `json:"production_ready"`
	Checks          []Check `json:"checks"`
}

func Run(ctx context.Context, workerPath string) Report {
	report := Report{Schema: "talos.doctor/1", Ready: true, ProductionReady: false}
	report.Checks = append(report.Checks,
		Check{Name: "runtime", Status: "passed", Details: map[string]any{"go": runtime.Version(), "os": runtime.GOOS, "arch": runtime.GOARCH}},
		gitCheck(),
		Check{Name: "key_store", Status: "unsupported", Details: map[string]any{"reason": "phase 0 has no production OS key-store adapter; plaintext fallback is forbidden"}},
	)

	if check, err := sqliteCheck(ctx); err != nil {
		report.Ready = false
		report.Checks = append(report.Checks, Check{Name: "encrypted_sqlite", Status: "failed", Details: map[string]any{"error": err.Error()}})
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
	return report
}

func gitCheck() Check {
	path, err := exec.LookPath("git")
	if err != nil {
		return Check{Name: "git", Status: "unavailable"}
	}
	return Check{Name: "git", Status: "passed", Details: map[string]any{"available": true, "executable": filepath.Base(path)}}
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
	return Check{Name: "encrypted_sqlite", Status: "passed", Details: map[string]any{"restart_roundtrip": true, "plaintext_marker_absent": true}}, nil
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
