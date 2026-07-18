package localvaultdb

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

const (
	restoreMetadataSchema = "talos.vault-restore-stage/1"
	restoreMetadataName   = "restore.json"
)

type restoreMetadataPayload struct {
	Schema                   string    `json:"schema"`
	BackupID                 string    `json:"backup_id"`
	VaultID                  string    `json:"vault_id"`
	CiphertextSHA256         string    `json:"ciphertext_sha256"`
	SourceApplicationVersion string    `json:"source_application_version"`
	MinimumRestoreVersion    string    `json:"minimum_restore_version"`
	SourceSchemaVersion      int       `json:"source_schema_version"`
	TargetSchemaVersion      int       `json:"target_schema_version"`
	CreatedAt                time.Time `json:"created_at"`
	StagedAt                 time.Time `json:"staged_at"`
	EncryptedSizeBytes       int64     `json:"encrypted_size_bytes"`
	DatabaseSizeBytes        int64     `json:"database_size_bytes"`
	ArtifactCount            int       `json:"artifact_count"`
	EventCount               int       `json:"event_count"`
}

type restoreMetadata struct {
	restoreMetadataPayload
	MAC string `json:"mac"`
}

type restorePaths struct {
	liveDatabase     string
	liveBlobs        string
	stageRoot        string
	stageDatabase    string
	stageBlobs       string
	previousRoot     string
	previousDatabase string
	previousBlobs    string
}

func (f *Factory) StageRestore(ctx context.Context, input vaultbackup.StageRestoreInput) (vaultbackup.Preflight, error) {
	if err := f.validateBackupInput(ctx, input.VaultID, input.KeyID, input.Key, input.ApplicationVersion); err != nil || !id.IsUUIDv7(input.ExpectedBackupID) || !validHash(input.ExpectedCiphertextSHA256) {
		return vaultbackup.Preflight{}, vaultbackup.ErrInvalidRequest
	}
	paths, err := f.restorePaths(input.VaultID)
	if err != nil {
		return vaultbackup.Preflight{}, err
	}
	if err := requireRegularFile(paths.liveDatabase); err != nil {
		return vaultbackup.Preflight{}, errors.Join(vaultbackup.ErrRestoreIncomplete, err)
	}
	for _, path := range []string{paths.stageRoot, paths.previousRoot} {
		if _, err := os.Lstat(path); err == nil {
			return vaultbackup.Preflight{}, vaultbackup.ErrRestorePending
		} else if !errors.Is(err, os.ErrNotExist) {
			return vaultbackup.Preflight{}, fmt.Errorf("inspect Vault restore staging state: %w", err)
		}
	}
	temporaryRoot, err := os.MkdirTemp(f.root, ".restore-stage-")
	if err != nil {
		return vaultbackup.Preflight{}, fmt.Errorf("create Vault restore staging directory: %w", err)
	}
	removeTemporary := true
	defer func() {
		if removeTemporary {
			_ = os.RemoveAll(temporaryRoot)
		}
	}()
	preflight, err := f.preflightBackupFile(ctx, vaultbackup.PreflightInput{
		VaultID: input.VaultID, KeyID: input.KeyID, Key: input.Key, Source: input.Source, ApplicationVersion: input.ApplicationVersion,
	}, true, temporaryRoot)
	if err != nil {
		return vaultbackup.Preflight{}, err
	}
	if preflight.BackupID != input.ExpectedBackupID || preflight.CiphertextSHA256 != input.ExpectedCiphertextSHA256 {
		return vaultbackup.Preflight{}, vaultbackup.ErrCorrupt
	}
	metadata := restoreMetadataPayload{
		Schema: restoreMetadataSchema, BackupID: preflight.BackupID, VaultID: preflight.VaultID,
		CiphertextSHA256: preflight.CiphertextSHA256, SourceApplicationVersion: preflight.SourceApplicationVersion,
		MinimumRestoreVersion: preflight.MinimumRestoreVersion, SourceSchemaVersion: preflight.SourceSchemaVersion,
		TargetSchemaVersion: preflight.TargetSchemaVersion, CreatedAt: preflight.CreatedAt, StagedAt: f.now().UTC(),
		EncryptedSizeBytes: preflight.EncryptedSizeBytes, DatabaseSizeBytes: preflight.DatabaseSizeBytes,
		ArtifactCount: preflight.ArtifactCount, EventCount: preflight.EventCount,
	}
	if err := writeRestoreMetadata(filepath.Join(temporaryRoot, restoreMetadataName), metadata, input.Key); err != nil {
		return vaultbackup.Preflight{}, err
	}
	if err := publishRestoreStage(temporaryRoot, paths.stageRoot); err != nil {
		return vaultbackup.Preflight{}, err
	}
	removeTemporary = false
	if err := f.afterRestoreStep("stage_published"); err != nil {
		return vaultbackup.Preflight{}, errors.Join(vaultbackup.ErrRestoreIncomplete, err)
	}
	return preflight, nil
}

func (f *Factory) ReconcileRestore(ctx context.Context, input vaultbackup.ReconcileRestoreInput) (vaultbackup.Restore, error) {
	if err := f.validateBackupInput(ctx, input.VaultID, input.KeyID, input.Key, input.ApplicationVersion); err != nil {
		return vaultbackup.Restore{}, err
	}
	paths, err := f.restorePaths(input.VaultID)
	if err != nil {
		return vaultbackup.Restore{}, err
	}
	metadata, err := readRestoreMetadata(filepath.Join(paths.stageRoot, restoreMetadataName), input.Key)
	if err != nil {
		return vaultbackup.Restore{}, errors.Join(vaultbackup.ErrRestoreIncomplete, err)
	}
	if metadata.VaultID != input.VaultID || compareSemanticVersion(input.ApplicationVersion, metadata.MinimumRestoreVersion) < 0 {
		return vaultbackup.Restore{}, vaultbackup.ErrRestoreIncomplete
	}
	if err := f.activateStagedRestore(paths); err != nil {
		return vaultbackup.Restore{}, errors.Join(vaultbackup.ErrRestoreIncomplete, err)
	}
	record, report, schemaVersion, validationErr := validateRestoreGeneration(ctx, paths.liveDatabase, input.VaultID, input.KeyID, input.Key)
	if validationErr != nil || record.Status != vault.StatusActive || report.ArtifactCount != metadata.ArtifactCount || report.EventCount != metadata.EventCount {
		if !restoreFullyPromoted(paths) {
			return vaultbackup.Restore{}, errors.Join(vaultbackup.ErrRestoreIncomplete, validationErr)
		}
		rollbackSchema, rollbackErr := f.rollbackRestore(ctx, paths, input)
		if rollbackErr != nil {
			return vaultbackup.Restore{}, errors.Join(vaultbackup.ErrRestoreIncomplete, validationErr, rollbackErr)
		}
		return restoreResult(metadata, vaultbackup.RestoreStateRolledBack, input.ApplicationVersion, rollbackSchema, f.now().UTC()), nil
	}
	return restoreResult(metadata, vaultbackup.RestoreStateRestored, input.ApplicationVersion, schemaVersion, f.now().UTC()), nil
}

func (f *Factory) FinalizeRestore(ctx context.Context, vaultID string) error {
	if f == nil || ctx == nil || ctx.Err() != nil {
		return vaultbackup.ErrInvalidRequest
	}
	paths, err := f.restorePaths(vaultID)
	if err != nil {
		return err
	}
	if err := requireRegularFile(paths.liveDatabase); err != nil {
		return errors.Join(vaultbackup.ErrRestoreIncomplete, err)
	}
	if err := removeRestoreRoot(paths.previousRoot); err != nil {
		return fmt.Errorf("remove previous Vault generation: %w", err)
	}
	if err := removeRestoreRoot(paths.stageRoot); err != nil {
		return fmt.Errorf("remove Vault restore staging generation: %w", err)
	}
	return nil
}

func (f *Factory) CleanupInactiveRestore(ctx context.Context, vaultID string) error {
	return f.FinalizeRestore(ctx, vaultID)
}

func (f *Factory) restorePaths(vaultID string) (restorePaths, error) {
	live, err := f.path(vaultID)
	if err != nil {
		return restorePaths{}, err
	}
	stageRoot := live + ".restore-stage"
	previousRoot := live + ".restore-previous"
	return restorePaths{
		liveDatabase: live, liveBlobs: live + ".blobs", stageRoot: stageRoot,
		stageDatabase: filepath.Join(stageRoot, databaseEntryName), stageBlobs: filepath.Join(stageRoot, databaseEntryName+".blobs"),
		previousRoot: previousRoot, previousDatabase: filepath.Join(previousRoot, databaseEntryName), previousBlobs: filepath.Join(previousRoot, databaseEntryName+".blobs"),
	}, nil
}

func (f *Factory) activateStagedRestore(paths restorePaths) error {
	if err := requireDirectory(paths.stageRoot); err != nil {
		return err
	}
	if err := ensureDirectory(paths.previousRoot); err != nil {
		return err
	}
	stageDatabaseExists, err := regularFileExists(paths.stageDatabase)
	if err != nil {
		return err
	}
	if stageDatabaseExists {
		if err := moveOldFile(paths.liveDatabase, paths.previousDatabase); err != nil {
			return err
		}
		for _, suffix := range []string{"-wal", "-shm"} {
			if err := moveOptionalOldFile(paths.liveDatabase+suffix, paths.previousDatabase+suffix); err != nil {
				return err
			}
		}
		if err := f.afterRestoreStep("previous_database_saved"); err != nil {
			return err
		}
	} else if err := requireRegularFile(paths.previousDatabase); err != nil {
		return err
	}
	stageBlobsExists, err := directoryExists(paths.stageBlobs)
	if err != nil {
		return err
	}
	if stageBlobsExists {
		if err := moveOptionalOldDirectory(paths.liveBlobs, paths.previousBlobs); err != nil {
			return err
		}
		if err := f.afterRestoreStep("previous_blobs_saved"); err != nil {
			return err
		}
	}
	if stageDatabaseExists {
		if _, err := os.Lstat(paths.liveDatabase); err == nil {
			return vaultbackup.ErrRestoreIncomplete
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(paths.stageDatabase, paths.liveDatabase); err != nil {
			return fmt.Errorf("activate restored Vault database: %w", err)
		}
		if err := f.afterRestoreStep("restored_database_activated"); err != nil {
			return err
		}
	}
	if stageBlobsExists {
		if _, err := os.Lstat(paths.liveBlobs); err == nil {
			return vaultbackup.ErrRestoreIncomplete
		} else if !errors.Is(err, os.ErrNotExist) {
			return err
		}
		if err := os.Rename(paths.stageBlobs, paths.liveBlobs); err != nil {
			return fmt.Errorf("activate restored Vault artifacts: %w", err)
		}
		if err := f.afterRestoreStep("restored_blobs_activated"); err != nil {
			return err
		}
	}
	if !restoreFullyPromoted(paths) {
		return vaultbackup.ErrRestoreIncomplete
	}
	return nil
}

func (f *Factory) rollbackRestore(ctx context.Context, paths restorePaths, input vaultbackup.ReconcileRestoreInput) (int, error) {
	if err := f.Purge(context.WithoutCancel(ctx), input.VaultID); err != nil {
		return 0, fmt.Errorf("remove invalid restored generation: %w", err)
	}
	if err := os.Rename(paths.previousDatabase, paths.liveDatabase); err != nil {
		return 0, fmt.Errorf("recover previous Vault database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if exists, err := regularFileExists(paths.previousDatabase + suffix); err != nil {
			return 0, err
		} else if exists {
			if err := os.Rename(paths.previousDatabase+suffix, paths.liveDatabase+suffix); err != nil {
				return 0, fmt.Errorf("recover previous Vault database sidecar: %w", err)
			}
		}
	}
	if exists, err := directoryExists(paths.previousBlobs); err != nil {
		return 0, err
	} else if exists {
		if err := os.Rename(paths.previousBlobs, paths.liveBlobs); err != nil {
			return 0, fmt.Errorf("recover previous Vault artifacts: %w", err)
		}
	}
	_, _, schemaVersion, err := validateRestoreGeneration(ctx, paths.liveDatabase, input.VaultID, input.KeyID, input.Key)
	if err != nil {
		return 0, fmt.Errorf("validate recovered previous Vault generation: %w", err)
	}
	return schemaVersion, nil
}

func validateRestoreGeneration(ctx context.Context, databasePath, vaultID, keyID string, key []byte) (vault.Record, sqliteevent.IntegrityReport, int, error) {
	sealer, err := envelope.NewSealer(keyID, key)
	if err != nil {
		return vault.Record{}, sqliteevent.IntegrityReport{}, 0, err
	}
	store, err := sqliteevent.Open(databasePath, sealer)
	if err != nil {
		sealer.Destroy()
		return vault.Record{}, sqliteevent.IntegrityReport{}, 0, err
	}
	record, recordErr := store.GetVault(ctx, vaultID)
	report, integrityErr := store.ValidateIntegrity(ctx, vaultID)
	checkpointErr := store.Checkpoint(ctx)
	closeErr := store.Close()
	sealer.Destroy()
	if recordErr != nil || integrityErr != nil || checkpointErr != nil || closeErr != nil || record.ID != vaultID {
		return vault.Record{}, sqliteevent.IntegrityReport{}, 0, errors.Join(recordErr, integrityErr, checkpointErr, closeErr)
	}
	snapshot, err := sqliteevent.InspectSnapshot(ctx, databasePath)
	if err != nil {
		return vault.Record{}, sqliteevent.IntegrityReport{}, 0, err
	}
	return record, report, snapshot.SchemaVersion, nil
}

func restoreResult(metadata restoreMetadataPayload, state vaultbackup.RestoreState, applicationVersion string, schemaVersion int, restoredAt time.Time) vaultbackup.Restore {
	return vaultbackup.Restore{
		Schema: vaultbackup.RestoreSchema, State: state, BackupID: metadata.BackupID, VaultID: metadata.VaultID,
		CiphertextSHA256: metadata.CiphertextSHA256, SourceApplicationVersion: metadata.SourceApplicationVersion,
		TargetApplicationVersion: applicationVersion, SourceSchemaVersion: metadata.SourceSchemaVersion,
		TargetSchemaVersion: schemaVersion, RestoredAt: restoredAt, ArtifactCount: metadata.ArtifactCount, EventCount: metadata.EventCount,
	}
}

func writeRestoreMetadata(path string, payload restoreMetadataPayload, key []byte) error {
	encoded, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("encode Vault restore metadata: %w", err)
	}
	metadataKey := deriveRestoreMetadataKey(key)
	defer clear(metadataKey)
	mac := hmac.New(sha256.New, metadataKey)
	_, _ = mac.Write(encoded)
	document := restoreMetadata{restoreMetadataPayload: payload, MAC: hex.EncodeToString(mac.Sum(nil))}
	encoded, err = json.Marshal(document)
	if err != nil {
		return fmt.Errorf("encode authenticated Vault restore metadata: %w", err)
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create Vault restore metadata: %w", err)
	}
	if _, err := file.Write(encoded); err != nil {
		_ = file.Close()
		return fmt.Errorf("write Vault restore metadata: %w", err)
	}
	if err := file.Sync(); err != nil {
		_ = file.Close()
		return fmt.Errorf("sync Vault restore metadata: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Vault restore metadata: %w", err)
	}
	return nil
}

func readRestoreMetadata(path string, key []byte) (restoreMetadataPayload, error) {
	info, err := os.Lstat(path)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxManifestBytes {
		return restoreMetadataPayload{}, vaultbackup.ErrRestoreIncomplete
	}
	encoded, err := os.ReadFile(path)
	if err != nil {
		return restoreMetadataPayload{}, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(encoded)))
	decoder.DisallowUnknownFields()
	var document restoreMetadata
	if err := decoder.Decode(&document); err != nil || decoder.Decode(&struct{}{}) != io.EOF {
		return restoreMetadataPayload{}, vaultbackup.ErrRestoreIncomplete
	}
	payloadBytes, err := json.Marshal(document.restoreMetadataPayload)
	if err != nil {
		return restoreMetadataPayload{}, vaultbackup.ErrRestoreIncomplete
	}
	provided, err := hex.DecodeString(document.MAC)
	if err != nil {
		return restoreMetadataPayload{}, vaultbackup.ErrRestoreIncomplete
	}
	metadataKey := deriveRestoreMetadataKey(key)
	defer clear(metadataKey)
	mac := hmac.New(sha256.New, metadataKey)
	_, _ = mac.Write(payloadBytes)
	if !hmac.Equal(provided, mac.Sum(nil)) || !validRestoreMetadata(document.restoreMetadataPayload) {
		return restoreMetadataPayload{}, vaultbackup.ErrRestoreIncomplete
	}
	return document.restoreMetadataPayload, nil
}

func deriveRestoreMetadataKey(key []byte) []byte {
	mac := hmac.New(sha256.New, key)
	_, _ = mac.Write([]byte("talos.vault-restore-metadata-key/v1"))
	return mac.Sum(nil)
}

func publishRestoreStage(sourceRoot, destinationRoot string) error {
	if err := os.Mkdir(destinationRoot, 0o700); err != nil {
		if errors.Is(err, os.ErrExist) {
			return vaultbackup.ErrRestorePending
		}
		return fmt.Errorf("reserve Vault restore staging generation: %w", err)
	}
	removeDestination := true
	defer func() {
		if removeDestination {
			_ = removeRestoreRoot(destinationRoot)
		}
	}()
	for _, name := range []string{databaseEntryName, databaseEntryName + ".blobs", restoreMetadataName} {
		if err := os.Rename(filepath.Join(sourceRoot, name), filepath.Join(destinationRoot, name)); err != nil {
			return fmt.Errorf("publish Vault restore staging component %s: %w", name, err)
		}
	}
	if err := os.Remove(sourceRoot); err != nil {
		return fmt.Errorf("remove empty Vault restore staging source: %w", err)
	}
	removeDestination = false
	return nil
}

func validRestoreMetadata(value restoreMetadataPayload) bool {
	return value.Schema == restoreMetadataSchema && id.IsUUIDv7(value.BackupID) && id.IsUUIDv7(value.VaultID) && validHash(value.CiphertextSHA256) &&
		validSemanticVersion(value.SourceApplicationVersion) && validSemanticVersion(value.MinimumRestoreVersion) && value.SourceSchemaVersion > 0 &&
		value.TargetSchemaVersion >= value.SourceSchemaVersion && !value.CreatedAt.IsZero() && !value.StagedAt.IsZero() &&
		value.EncryptedSizeBytes > 0 && value.DatabaseSizeBytes > 0 && value.ArtifactCount >= 0 && value.EventCount > 0
}

func restoreFullyPromoted(paths restorePaths) bool {
	stageDatabase, _ := regularFileExists(paths.stageDatabase)
	stageBlobs, _ := directoryExists(paths.stageBlobs)
	liveDatabase, _ := regularFileExists(paths.liveDatabase)
	liveBlobs, _ := directoryExists(paths.liveBlobs)
	return !stageDatabase && !stageBlobs && liveDatabase && liveBlobs
}

func moveOldFile(source, destination string) error {
	if exists, err := regularFileExists(destination); err != nil {
		return err
	} else if exists {
		return nil
	}
	if err := requireRegularFile(source); err != nil {
		return err
	}
	if err := os.Rename(source, destination); err != nil {
		return fmt.Errorf("preserve previous Vault database: %w", err)
	}
	return nil
}

func moveOptionalOldFile(source, destination string) error {
	if exists, err := regularFileExists(destination); err != nil {
		return err
	} else if exists {
		return nil
	}
	exists, err := regularFileExists(source)
	if err != nil || !exists {
		return err
	}
	return os.Rename(source, destination)
}

func moveOptionalOldDirectory(source, destination string) error {
	if exists, err := directoryExists(destination); err != nil {
		return err
	} else if exists {
		return nil
	}
	exists, err := directoryExists(source)
	if err != nil || !exists {
		return err
	}
	return os.Rename(source, destination)
}

func requireRegularFile(path string) error {
	exists, err := regularFileExists(path)
	if err != nil {
		return err
	}
	if !exists {
		return vaultbackup.ErrRestoreIncomplete
	}
	return nil
}

func requireDirectory(path string) error {
	exists, err := directoryExists(path)
	if err != nil {
		return err
	}
	if !exists {
		return vaultbackup.ErrRestoreIncomplete
	}
	return nil
}

func ensureDirectory(path string) error {
	if exists, err := directoryExists(path); err != nil {
		return err
	} else if exists {
		return nil
	}
	return os.Mkdir(path, 0o700)
}

func regularFileExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return false, vaultbackup.ErrRestoreIncomplete
	}
	return true, nil
}

func directoryExists(path string) (bool, error) {
	info, err := os.Lstat(path)
	if errors.Is(err, os.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return false, vaultbackup.ErrRestoreIncomplete
	}
	return true, nil
}

func removeRestoreRoot(root string) error {
	exists, err := directoryExists(root)
	if err != nil || !exists {
		return err
	}
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		path := filepath.Join(root, entry.Name())
		switch entry.Name() {
		case restoreMetadataName, databaseEntryName, databaseEntryName + "-wal", databaseEntryName + "-shm":
			if entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return vaultbackup.ErrRestoreIncomplete
			}
			if err := os.Remove(path); err != nil {
				return err
			}
		case databaseEntryName + ".blobs":
			if !entry.IsDir() || entry.Type()&os.ModeSymlink != 0 {
				return vaultbackup.ErrRestoreIncomplete
			}
			children, err := os.ReadDir(path)
			if err != nil {
				return err
			}
			for _, child := range children {
				if child.IsDir() || child.Type()&os.ModeSymlink != 0 || !validPurgeArtifactName(child.Name()) {
					return vaultbackup.ErrRestoreIncomplete
				}
				if err := os.Remove(filepath.Join(path, child.Name())); err != nil {
					return err
				}
			}
			if err := os.Remove(path); err != nil {
				return err
			}
		default:
			return vaultbackup.ErrRestoreIncomplete
		}
	}
	return os.Remove(root)
}

func (f *Factory) afterRestoreStep(step string) error {
	if f.restoreHook == nil {
		return nil
	}
	return f.restoreHook(step)
}
