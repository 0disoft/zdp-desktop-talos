package sqliteevent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"modernc.org/sqlite"
)

type SnapshotArtifact struct {
	ArtifactID     string
	StorageName    string
	CiphertextHash string
	SizeBytes      int64
}

type SnapshotInfo struct {
	SchemaVersion int
	VaultID       string
	Artifacts     []SnapshotArtifact
}

type IntegrityReport struct {
	EventCount    int
	ArtifactCount int
}

type onlineBackuper interface {
	NewBackup(string) (*sqlite.Backup, error)
}

func CurrentSchemaVersion() int {
	return currentSchemaVersion
}

func (s *Store) OnlineBackup(ctx context.Context, destination string) error {
	if s == nil || s.db == nil || ctx == nil || strings.TrimSpace(destination) == "" || !filepath.IsAbs(destination) {
		return fmt.Errorf("online backup destination is invalid")
	}
	if _, err := os.Lstat(destination); err == nil {
		return fmt.Errorf("online backup destination already exists")
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect online backup destination: %w", err)
	}

	connection, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("reserve sqlite backup connection: %w", err)
	}
	backupErr := connection.Raw(func(driverConnection any) error {
		backuper, ok := driverConnection.(onlineBackuper)
		if !ok {
			return errors.New("sqlite driver does not expose online backup")
		}
		backup, err := backuper.NewBackup(destination)
		if err != nil {
			return fmt.Errorf("initialize sqlite online backup: %w", err)
		}
		more, stepErr := backup.Step(-1)
		finishErr := backup.Finish()
		if stepErr != nil || finishErr != nil {
			var failures []error
			if stepErr != nil {
				failures = append(failures, fmt.Errorf("copy sqlite online backup: %w", stepErr))
			}
			if finishErr != nil {
				failures = append(failures, fmt.Errorf("finish sqlite online backup: %w", finishErr))
			}
			return errors.Join(failures...)
		}
		if more {
			return errors.New("sqlite online backup did not reach a terminal state")
		}
		return nil
	})
	closeConnectionErr := connection.Close()
	if backupErr != nil || closeConnectionErr != nil {
		_ = os.Remove(destination)
		return errors.Join(backupErr, closeConnectionErr)
	}
	file, err := os.OpenFile(destination, os.O_RDWR, 0)
	if err != nil {
		_ = os.Remove(destination)
		return fmt.Errorf("open sqlite backup for sync: %w", err)
	}
	syncErr := file.Sync()
	closeErr := file.Close()
	if syncErr != nil || closeErr != nil {
		_ = os.Remove(destination)
		var failures []error
		if syncErr != nil {
			failures = append(failures, fmt.Errorf("sync sqlite backup: %w", syncErr))
		}
		if closeErr != nil {
			failures = append(failures, fmt.Errorf("close sqlite backup: %w", closeErr))
		}
		return errors.Join(failures...)
	}
	return nil
}

func InspectSnapshot(ctx context.Context, path string) (SnapshotInfo, error) {
	if ctx == nil || strings.TrimSpace(path) == "" || !filepath.IsAbs(path) {
		return SnapshotInfo{}, errors.New("snapshot path is invalid")
	}
	database, err := sql.Open("sqlite", path)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("open sqlite snapshot: %w", err)
	}
	database.SetMaxOpenConns(1)
	database.SetMaxIdleConns(1)
	defer database.Close()
	if _, err := database.ExecContext(ctx, "PRAGMA query_only=ON"); err != nil {
		return SnapshotInfo{}, fmt.Errorf("make sqlite snapshot read-only: %w", err)
	}
	if err := quickCheck(ctx, database); err != nil {
		return SnapshotInfo{}, err
	}

	var result SnapshotInfo
	if err := database.QueryRowContext(ctx, "PRAGMA user_version").Scan(&result.SchemaVersion); err != nil {
		return SnapshotInfo{}, fmt.Errorf("read sqlite snapshot schema version: %w", err)
	}
	if result.SchemaVersion < 1 || result.SchemaVersion > currentSchemaVersion {
		return SnapshotInfo{}, ErrUnsupportedSchema
	}
	if err := database.QueryRowContext(ctx, `SELECT vault_id FROM vault_states WHERE status = 'active' ORDER BY vault_id LIMIT 1`).Scan(&result.VaultID); err != nil {
		return SnapshotInfo{}, fmt.Errorf("read sqlite snapshot Vault identity: %w", err)
	}
	rows, err := database.QueryContext(ctx, `SELECT artifact_id, storage_name, ciphertext_hash, size_bytes
		FROM artifacts WHERE state = 'ready' ORDER BY storage_name, artifact_id`)
	if err != nil {
		return SnapshotInfo{}, fmt.Errorf("list sqlite snapshot artifacts: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var artifact SnapshotArtifact
		if err := rows.Scan(&artifact.ArtifactID, &artifact.StorageName, &artifact.CiphertextHash, &artifact.SizeBytes); err != nil {
			return SnapshotInfo{}, fmt.Errorf("scan sqlite snapshot artifact: %w", err)
		}
		result.Artifacts = append(result.Artifacts, artifact)
	}
	if err := rows.Err(); err != nil {
		return SnapshotInfo{}, fmt.Errorf("iterate sqlite snapshot artifacts: %w", err)
	}
	return result, nil
}

func (s *Store) ValidateIntegrity(ctx context.Context, vaultID string) (IntegrityReport, error) {
	if s == nil || s.db == nil || ctx == nil || strings.TrimSpace(vaultID) == "" {
		return IntegrityReport{}, errors.New("integrity validation input is invalid")
	}
	if err := quickCheck(ctx, s.db); err != nil {
		return IntegrityReport{}, err
	}

	rows, err := s.db.QueryContext(ctx, `SELECT event_id, vault_id, event_type, schema_version, sensitivity, payload_envelope, occurred_at
		FROM events WHERE vault_id = ? ORDER BY occurred_at, event_id`, vaultID)
	if err != nil {
		return IntegrityReport{}, fmt.Errorf("list encrypted events for integrity validation: %w", err)
	}
	var report IntegrityReport
	for rows.Next() {
		record, err := s.scanRecord(rows)
		if err != nil {
			_ = rows.Close()
			return IntegrityReport{}, fmt.Errorf("validate encrypted event: %w", err)
		}
		clear(record.Payload)
		report.EventCount++
	}
	if err := rows.Close(); err != nil {
		return IntegrityReport{}, fmt.Errorf("close encrypted event validation rows: %w", err)
	}
	if err := rows.Err(); err != nil {
		return IntegrityReport{}, fmt.Errorf("iterate encrypted events for validation: %w", err)
	}

	artifactRows, err := s.db.QueryContext(ctx, `SELECT artifact_id FROM artifacts WHERE vault_id = ? AND state = 'ready' ORDER BY artifact_id`, vaultID)
	if err != nil {
		return IntegrityReport{}, fmt.Errorf("list encrypted artifacts for integrity validation: %w", err)
	}
	var artifactIDs []string
	for artifactRows.Next() {
		var artifactID string
		if err := artifactRows.Scan(&artifactID); err != nil {
			_ = artifactRows.Close()
			return IntegrityReport{}, fmt.Errorf("scan encrypted artifact id: %w", err)
		}
		artifactIDs = append(artifactIDs, artifactID)
	}
	if err := artifactRows.Close(); err != nil {
		return IntegrityReport{}, fmt.Errorf("close encrypted artifact validation rows: %w", err)
	}
	if err := artifactRows.Err(); err != nil {
		return IntegrityReport{}, fmt.Errorf("iterate encrypted artifact ids: %w", err)
	}
	for _, artifactID := range artifactIDs {
		_, payload, err := s.GetArtifact(ctx, artifactID)
		if err != nil {
			if errors.Is(err, artifactstore.ErrCorrupt) {
				return IntegrityReport{}, fmt.Errorf("validate encrypted artifact %s: %w", artifactID, artifactstore.ErrCorrupt)
			}
			return IntegrityReport{}, fmt.Errorf("load encrypted artifact %s: %w", artifactID, err)
		}
		clear(payload)
		report.ArtifactCount++
	}
	return report, nil
}

func quickCheck(ctx context.Context, database interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}) error {
	rows, err := database.QueryContext(ctx, "PRAGMA quick_check")
	if err != nil {
		return fmt.Errorf("run sqlite quick_check: %w", err)
	}
	defer rows.Close()
	count := 0
	for rows.Next() {
		var result string
		if err := rows.Scan(&result); err != nil {
			return fmt.Errorf("scan sqlite quick_check result: %w", err)
		}
		count++
		if result != "ok" {
			return fmt.Errorf("sqlite quick_check failed: %s", result)
		}
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("iterate sqlite quick_check result: %w", err)
	}
	if count != 1 {
		return fmt.Errorf("sqlite quick_check returned %d rows", count)
	}
	return nil
}
