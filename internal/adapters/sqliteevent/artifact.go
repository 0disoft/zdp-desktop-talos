package sqliteevent

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"mime"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/artifact"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/artifactstore"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

const (
	maxContentTypeBytes = 255
	maxCiphertextBytes  = 2*artifact.MaxPayloadBytes + 4096
)

type artifactRow struct {
	record      artifact.Record
	storageName string
	stagingName string
	state       string
}

func (s *Store) PutArtifact(ctx context.Context, input artifactstore.PutInput) (artifact.Record, error) {
	if err := validateArtifactInput(input); err != nil {
		return artifact.Record{}, err
	}
	createdAt := s.now().UTC()
	artifactID, err := id.UUIDv7(createdAt, s.random)
	if err != nil {
		return artifact.Record{}, fmt.Errorf("generate artifact id: %w", err)
	}
	contentSum := sha256.Sum256(input.Payload)
	encoded, err := s.sealer.Seal(input.Payload, artifactAAD(artifactID, input.VaultID, input.SchemaVersion, input.Sensitivity))
	if err != nil {
		return artifact.Record{}, fmt.Errorf("encrypt artifact: %w", err)
	}
	if len(encoded) > maxCiphertextBytes {
		return artifact.Record{}, artifactstore.ErrInvalidInput
	}
	ciphertextSum := sha256.Sum256(encoded)
	record := artifact.Record{
		ID: artifactID, VaultID: input.VaultID, SchemaVersion: input.SchemaVersion,
		Sensitivity: input.Sensitivity, ContentType: input.ContentType, SizeBytes: int64(len(input.Payload)),
		ContentHash: hex.EncodeToString(contentSum[:]), CiphertextHash: hex.EncodeToString(ciphertextSum[:]), CreatedAt: createdAt,
	}
	if err := record.Validate(); err != nil {
		return artifact.Record{}, err
	}
	stagingName := artifactID + ".stage"
	storageName := record.CiphertextHash + ".blob"
	stagingPath := filepath.Join(s.blobRoot, stagingName)
	if err := writeExclusiveSynced(stagingPath, encoded); err != nil {
		return artifact.Record{}, err
	}
	if err := s.insertStagedArtifact(ctx, record, storageName, stagingName); err != nil {
		_ = os.Remove(stagingPath)
		return artifact.Record{}, err
	}
	if err := s.promoteArtifact(ctx, artifactRow{record: record, storageName: storageName, stagingName: stagingName, state: "staged"}); err != nil {
		return artifact.Record{}, err
	}
	return record, nil
}

func (s *Store) GetArtifact(ctx context.Context, artifactID string) (artifact.Record, []byte, error) {
	row, err := s.readArtifact(ctx, artifactID, true)
	if err != nil {
		return artifact.Record{}, nil, err
	}
	if err := validateArtifactNames(row); err != nil {
		return artifact.Record{}, nil, err
	}
	encoded, err := readBoundedCiphertext(filepath.Join(s.blobRoot, row.storageName))
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return artifact.Record{}, nil, artifactstore.ErrCorrupt
		}
		return artifact.Record{}, nil, fmt.Errorf("read artifact ciphertext: %w", err)
	}
	if hashBytes(encoded) != row.record.CiphertextHash {
		return artifact.Record{}, nil, artifactstore.ErrCorrupt
	}
	payload, err := s.sealer.Open(encoded, artifactAAD(row.record.ID, row.record.VaultID, row.record.SchemaVersion, row.record.Sensitivity))
	if err != nil {
		return artifact.Record{}, nil, fmt.Errorf("%w: decrypt artifact: %v", artifactstore.ErrCorrupt, err)
	}
	if int64(len(payload)) != row.record.SizeBytes || hashBytes(payload) != row.record.ContentHash {
		clear(payload)
		return artifact.Record{}, nil, artifactstore.ErrCorrupt
	}
	return row.record, payload, nil
}

func (s *Store) ReconcileArtifacts(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, artifactSelect+` WHERE state = 'staged' ORDER BY created_at, artifact_id`)
	if err != nil {
		return fmt.Errorf("list staged artifacts: %w", err)
	}
	var staged []artifactRow
	for rows.Next() {
		row, err := scanArtifact(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		staged = append(staged, row)
	}
	if err := rows.Close(); err != nil {
		return fmt.Errorf("close staged artifact rows: %w", err)
	}
	for _, row := range staged {
		if err := s.reconcileArtifact(ctx, row); err != nil {
			return err
		}
	}
	return s.removeOrphanStages(ctx)
}

func (s *Store) insertStagedArtifact(ctx context.Context, record artifact.Record, storageName, stagingName string) error {
	_, err := s.db.ExecContext(ctx, `INSERT INTO artifacts(
		artifact_id, vault_id, schema_version, sensitivity, content_type, size_bytes, content_hash,
		ciphertext_hash, storage_name, staging_name, state, created_at
	) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?, 'staged', ?)`, record.ID, record.VaultID, record.SchemaVersion,
		string(record.Sensitivity), record.ContentType, record.SizeBytes, record.ContentHash, record.CiphertextHash,
		storageName, stagingName, record.CreatedAt.Format(time.RFC3339Nano))
	if err != nil {
		return fmt.Errorf("record staged artifact: %w", err)
	}
	return nil
}

func (s *Store) promoteArtifact(ctx context.Context, row artifactRow) error {
	if err := validateArtifactNames(row); err != nil {
		return err
	}
	stagingPath := filepath.Join(s.blobRoot, row.stagingName)
	storagePath := filepath.Join(s.blobRoot, row.storageName)
	if existing, err := readBoundedCiphertext(storagePath); err == nil {
		if hashBytes(existing) != row.record.CiphertextHash {
			return artifactstore.ErrCorrupt
		}
		_ = os.Remove(stagingPath)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect artifact destination: %w", err)
	} else if err := os.Rename(stagingPath, storagePath); err != nil {
		return fmt.Errorf("promote staged artifact: %w", err)
	}
	result, err := s.db.ExecContext(ctx, `UPDATE artifacts SET state = 'ready' WHERE artifact_id = ? AND state = 'staged'`, row.record.ID)
	if err != nil {
		return fmt.Errorf("mark artifact ready: %w", err)
	}
	affected, err := result.RowsAffected()
	if err != nil || affected != 1 {
		return fmt.Errorf("mark artifact ready affected %d rows: %w", affected, err)
	}
	return nil
}

func (s *Store) reconcileArtifact(ctx context.Context, row artifactRow) error {
	if err := validateArtifactNames(row); err != nil {
		return err
	}
	storagePath := filepath.Join(s.blobRoot, row.storageName)
	stagingPath := filepath.Join(s.blobRoot, row.stagingName)
	if _, err := os.Stat(storagePath); err == nil {
		return s.promoteArtifact(ctx, row)
	} else if !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("inspect staged artifact destination: %w", err)
	}
	encoded, err := readBoundedCiphertext(stagingPath)
	if errors.Is(err, os.ErrNotExist) {
		_, deleteErr := s.db.ExecContext(ctx, `DELETE FROM artifacts WHERE artifact_id = ? AND state = 'staged'`, row.record.ID)
		if deleteErr != nil {
			return fmt.Errorf("remove abandoned staged artifact row: %w", deleteErr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("read staged artifact: %w", err)
	}
	if hashBytes(encoded) != row.record.CiphertextHash {
		return artifactstore.ErrCorrupt
	}
	return s.promoteArtifact(ctx, row)
}

func (s *Store) readArtifact(ctx context.Context, artifactID string, readyOnly bool) (artifactRow, error) {
	if !id.IsUUIDv7(artifactID) {
		return artifactRow{}, artifactstore.ErrNotFound
	}
	query := artifactSelect + ` WHERE artifact_id = ?`
	if readyOnly {
		query += ` AND state = 'ready'`
	}
	row, err := scanArtifact(s.db.QueryRowContext(ctx, query, artifactID))
	if errors.Is(err, sql.ErrNoRows) {
		return artifactRow{}, artifactstore.ErrNotFound
	}
	return row, err
}

const artifactSelect = `SELECT artifact_id, vault_id, schema_version, sensitivity, content_type, size_bytes,
	content_hash, ciphertext_hash, storage_name, staging_name, state, created_at FROM artifacts`

func scanArtifact(scanner scanner) (artifactRow, error) {
	var row artifactRow
	var sensitivity, createdAt string
	if err := scanner.Scan(&row.record.ID, &row.record.VaultID, &row.record.SchemaVersion, &sensitivity,
		&row.record.ContentType, &row.record.SizeBytes, &row.record.ContentHash, &row.record.CiphertextHash,
		&row.storageName, &row.stagingName, &row.state, &createdAt); err != nil {
		return artifactRow{}, err
	}
	row.record.Sensitivity = event.Sensitivity(sensitivity)
	parsed, err := time.Parse(time.RFC3339Nano, createdAt)
	if err != nil {
		return artifactRow{}, artifactstore.ErrCorrupt
	}
	row.record.CreatedAt = parsed
	if err := row.record.Validate(); err != nil {
		return artifactRow{}, fmt.Errorf("%w: %v", artifactstore.ErrCorrupt, err)
	}
	return row, nil
}

func validateArtifactInput(input artifactstore.PutInput) error {
	if input.VaultID == "" || input.SchemaVersion < 1 || input.ContentType == "" || len(input.ContentType) > maxContentTypeBytes || len(input.Payload) < 1 || len(input.Payload) > artifact.MaxPayloadBytes {
		return artifactstore.ErrInvalidInput
	}
	if mediaType, _, err := mime.ParseMediaType(input.ContentType); err != nil || mediaType == "" {
		return artifactstore.ErrInvalidInput
	}
	if input.Sensitivity == event.SensitivitySecret || input.Sensitivity.Validate() != nil {
		return artifactstore.ErrInvalidInput
	}
	return nil
}

func validateArtifactNames(row artifactRow) error {
	if !id.IsUUIDv7(row.record.ID) || row.stagingName != row.record.ID+".stage" || row.storageName != row.record.CiphertextHash+".blob" || len(row.record.CiphertextHash) != sha256.Size*2 {
		return artifactstore.ErrCorrupt
	}
	if _, err := hex.DecodeString(row.record.CiphertextHash); err != nil {
		return artifactstore.ErrCorrupt
	}
	return nil
}

func writeExclusiveSynced(path string, payload []byte) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return fmt.Errorf("create artifact staging file: %w", err)
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(path)
		}
	}()
	if _, err := file.Write(payload); err != nil {
		return fmt.Errorf("write artifact staging file: %w", err)
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync artifact staging file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close artifact staging file: %w", err)
	}
	remove = false
	return nil
}

func ensureOwnedDirectory(path string) error {
	if err := os.MkdirAll(path, 0o700); err != nil {
		return fmt.Errorf("create artifact directory: %w", err)
	}
	return nil
}

func (s *Store) removeOrphanStages(ctx context.Context) error {
	rows, err := s.db.QueryContext(ctx, `SELECT staging_name FROM artifacts WHERE state = 'staged'`)
	if err != nil {
		return fmt.Errorf("list referenced staging files: %w", err)
	}
	referenced := map[string]struct{}{}
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			_ = rows.Close()
			return err
		}
		referenced[name] = struct{}{}
	}
	if err := rows.Close(); err != nil {
		return err
	}
	entries, err := os.ReadDir(s.blobRoot)
	if err != nil {
		return fmt.Errorf("scan artifact directory: %w", err)
	}
	for _, entry := range entries {
		name := entry.Name()
		if entry.IsDir() || !strings.HasSuffix(name, ".stage") {
			continue
		}
		if _, ok := referenced[name]; ok {
			continue
		}
		artifactID := strings.TrimSuffix(name, ".stage")
		if !id.IsUUIDv7(artifactID) {
			continue
		}
		if err := os.Remove(filepath.Join(s.blobRoot, name)); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("remove orphan artifact staging file: %w", err)
		}
	}
	return nil
}

func artifactAAD(artifactID, vaultID string, schemaVersion int, sensitivity event.Sensitivity) envelope.AAD {
	return envelope.AAD{VaultID: vaultID, ObjectID: artifactID, SchemaVersion: schemaVersion, Sensitivity: string(sensitivity)}
}

func hashBytes(payload []byte) string {
	sum := sha256.Sum256(payload)
	return hex.EncodeToString(sum[:])
}

func readBoundedCiphertext(path string) ([]byte, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return nil, err
	}
	if info.Size() < 1 || info.Size() > maxCiphertextBytes {
		return nil, artifactstore.ErrCorrupt
	}
	payload, err := io.ReadAll(io.LimitReader(file, maxCiphertextBytes+1))
	if err != nil {
		return nil, err
	}
	if len(payload) > maxCiphertextBytes {
		return nil, artifactstore.ErrCorrupt
	}
	return payload, nil
}
