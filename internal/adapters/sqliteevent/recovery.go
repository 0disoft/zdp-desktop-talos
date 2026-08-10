package sqliteevent

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"time"
)

const (
	legacyPortableRecoveryKey                 = "legacy-portable-state/v24"
	legacyPortableRecoveryVersion             = 1
	legacyPortableRecoveryLegacySchemaVersion = 24
)

func (s *Store) reconcileLegacyPortableState(ctx context.Context) error {
	completed, err := s.legacyRecoveryCompleted(ctx)
	if err != nil {
		return err
	}
	if completed {
		return nil
	}
	steps := []func(context.Context) error{
		s.ReconcileWorkspaceMappings,
		s.ReconcileTaskSyncSnapshots,
		s.ReconcileMemoryWorkspaceIDs,
		s.ReconcileMemorySyncSnapshots,
	}
	for _, step := range steps {
		if err := step(ctx); err != nil {
			return err
		}
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO recovery_markers(recovery_key, completed_schema_version, completed_at) VALUES(?, ?, ?) ON CONFLICT(recovery_key) DO NOTHING`, legacyPortableRecoveryKey, legacyPortableRecoveryVersion, s.now().UTC().Format(time.RFC3339Nano)); err != nil {
		return fmt.Errorf("record legacy portable-state recovery: %w", err)
	}
	return nil
}

func (s *Store) legacyRecoveryCompleted(ctx context.Context) (bool, error) {
	var schemaVersion int
	var completedAt string
	err := s.db.QueryRowContext(ctx, `SELECT completed_schema_version, completed_at FROM recovery_markers WHERE recovery_key = ?`, legacyPortableRecoveryKey).Scan(&schemaVersion, &completedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("read legacy portable-state recovery marker: %w", err)
	}
	if schemaVersion == legacyPortableRecoveryLegacySchemaVersion {
		if _, err := s.db.ExecContext(ctx, `UPDATE recovery_markers SET completed_schema_version = ? WHERE recovery_key = ? AND completed_schema_version = ?`, legacyPortableRecoveryVersion, legacyPortableRecoveryKey, legacyPortableRecoveryLegacySchemaVersion); err != nil {
			return false, fmt.Errorf("normalize legacy portable-state recovery marker: %w", err)
		}
		schemaVersion = legacyPortableRecoveryVersion
	}
	if schemaVersion != legacyPortableRecoveryVersion {
		return false, fmt.Errorf("legacy portable-state recovery marker schema mismatch: %d", schemaVersion)
	}
	if _, err := time.Parse(time.RFC3339Nano, completedAt); err != nil {
		return false, fmt.Errorf("legacy portable-state recovery marker time is invalid: %w", err)
	}
	return true, nil
}
