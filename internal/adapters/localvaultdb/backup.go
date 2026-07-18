package localvaultdb

import (
	"archive/tar"
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/vault"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultbackup"
	"github.com/0disoft/zdp-desktop-talos/internal/security/backupstream"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

const (
	manifestEntryName       = "manifest.json"
	databaseEntryName       = "vault.db"
	blobEntryPrefix         = "vault.db.blobs/"
	maxArtifactCount        = 100_000
	maxManifestBytes        = 32 << 20
	maxPlaintextBackupBytes = int64(64) << 30
	maxEncryptedBackupBytes = int64(65) << 30
	maxApplicationVersion   = 64
)

var semanticVersionPattern = regexp.MustCompile(`^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$`)

type archiveSource struct {
	record vaultbackup.FileRecord
	path   string
}

func (f *Factory) CreateBackup(ctx context.Context, input vaultbackup.CreateInput) (vaultbackup.Receipt, error) {
	if err := f.validateBackupInput(ctx, input.VaultID, input.KeyID, input.Key, input.ApplicationVersion); err != nil {
		return vaultbackup.Receipt{}, err
	}
	destination, err := canonicalBackupDestination(input.Destination)
	if err != nil {
		return vaultbackup.Receipt{}, err
	}
	if _, err := os.Lstat(destination); err == nil {
		return vaultbackup.Receipt{}, vaultbackup.ErrAlreadyExists
	} else if !errors.Is(err, os.ErrNotExist) {
		return vaultbackup.Receipt{}, fmt.Errorf("inspect Vault backup destination: %w", err)
	}
	if err := os.MkdirAll(f.root, 0o700); err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("create Vault backup staging root: %w", err)
	}

	stagingRoot, err := os.MkdirTemp(f.root, ".backup-create-")
	if err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("create Vault backup staging directory: %w", err)
	}
	defer os.RemoveAll(stagingRoot)
	databaseSnapshot := filepath.Join(stagingRoot, databaseEntryName)
	if err := f.createDatabaseSnapshot(ctx, input.VaultID, input.KeyID, input.Key, databaseSnapshot); err != nil {
		return vaultbackup.Receipt{}, err
	}
	snapshot, err := sqliteevent.InspectSnapshot(ctx, databaseSnapshot)
	if err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("inspect Vault database snapshot: %w", err)
	}
	if snapshot.VaultID != input.VaultID {
		return vaultbackup.Receipt{}, vaultbackup.ErrWrongVault
	}
	createdAt := input.CreatedAt.UTC()
	if createdAt.IsZero() {
		createdAt = f.now().UTC()
	}
	backupID, err := id.UUIDv7(createdAt, f.random)
	if err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("generate Vault backup ID: %w", err)
	}
	sources, databaseRecord, err := f.backupSources(databaseSnapshot, snapshot)
	if err != nil {
		return vaultbackup.Receipt{}, err
	}
	artifactRecords := make([]vaultbackup.FileRecord, 0, len(sources)-1)
	for _, source := range sources[1:] {
		artifactRecords = append(artifactRecords, source.record)
	}
	manifest := vaultbackup.Manifest{
		Schema:                   vaultbackup.ManifestSchema,
		BackupID:                 backupID,
		VaultID:                  input.VaultID,
		KeyID:                    input.KeyID,
		SourceApplicationVersion: input.ApplicationVersion,
		MinimumRestoreVersion:    input.ApplicationVersion,
		SourceSchemaVersion:      snapshot.SchemaVersion,
		CreatedAt:                createdAt,
		Database:                 databaseRecord,
		Artifacts:                artifactRecords,
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("encode Vault backup manifest: %w", err)
	}
	if len(manifestBytes) == 0 || len(manifestBytes) > maxManifestBytes {
		return vaultbackup.Receipt{}, vaultbackup.ErrCorrupt
	}

	stageFile, err := os.CreateTemp(filepath.Dir(destination), "."+filepath.Base(destination)+"-*.stage")
	if err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("create encrypted Vault backup staging file: %w", err)
	}
	stagePath := stageFile.Name()
	removeStage := true
	defer func() {
		_ = stageFile.Close()
		if removeStage {
			_ = os.Remove(stagePath)
		}
	}()
	if err := stageFile.Chmod(0o600); err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("protect Vault backup staging file: %w", err)
	}
	if err := writeEncryptedArchive(stageFile, input.KeyID, input.Key, createdAt, manifestBytes, sources); err != nil {
		return vaultbackup.Receipt{}, err
	}
	if err := stageFile.Sync(); err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("sync encrypted Vault backup: %w", err)
	}
	if err := stageFile.Close(); err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("close encrypted Vault backup: %w", err)
	}

	preflight, err := f.preflightBackupFile(ctx, vaultbackup.PreflightInput{
		VaultID: input.VaultID, KeyID: input.KeyID, Key: input.Key, Source: stagePath, ApplicationVersion: input.ApplicationVersion,
	}, false)
	if err != nil {
		return vaultbackup.Receipt{}, fmt.Errorf("verify newly created Vault backup: %w", err)
	}
	if err := promoteBackupExclusive(stagePath, destination); err != nil {
		return vaultbackup.Receipt{}, err
	}
	removeStage = false
	return vaultbackup.Receipt{
		Schema:                   vaultbackup.ReceiptSchema,
		BackupID:                 preflight.BackupID,
		VaultID:                  preflight.VaultID,
		Path:                     destination,
		SourceApplicationVersion: preflight.SourceApplicationVersion,
		SourceSchemaVersion:      preflight.SourceSchemaVersion,
		CreatedAt:                preflight.CreatedAt,
		EncryptedSizeBytes:       preflight.EncryptedSizeBytes,
		CiphertextSHA256:         preflight.CiphertextSHA256,
		DatabaseSizeBytes:        preflight.DatabaseSizeBytes,
		ArtifactCount:            preflight.ArtifactCount,
		EventCount:               preflight.EventCount,
	}, nil
}

func (f *Factory) PreflightBackup(ctx context.Context, input vaultbackup.PreflightInput) (vaultbackup.Preflight, error) {
	if err := f.validateBackupInput(ctx, input.VaultID, input.KeyID, input.Key, input.ApplicationVersion); err != nil {
		return vaultbackup.Preflight{}, err
	}
	source, err := canonicalExistingBackup(input.Source)
	if err != nil {
		return vaultbackup.Preflight{}, err
	}
	input.Source = source
	return f.preflightBackupFile(ctx, input, true)
}

func (f *Factory) validateBackupInput(ctx context.Context, vaultID, keyID string, key []byte, applicationVersion string) error {
	if f == nil || strings.TrimSpace(f.root) == "" || ctx == nil || ctx.Err() != nil || !id.IsUUIDv7(vaultID) || strings.TrimSpace(keyID) == "" || len(keyID) > 128 || len(key) != 32 || !validSemanticVersion(applicationVersion) {
		return vaultbackup.ErrInvalidRequest
	}
	return nil
}

func (f *Factory) createDatabaseSnapshot(ctx context.Context, vaultID, keyID string, key []byte, destination string) error {
	sealer, err := envelope.NewSealer(keyID, key)
	if err != nil {
		return err
	}
	defer sealer.Destroy()
	sourcePath, err := f.path(vaultID)
	if err != nil {
		return err
	}
	store, err := sqliteevent.Open(sourcePath, sealer)
	if err != nil {
		return fmt.Errorf("open Vault for online backup: %w", err)
	}
	backupErr := store.OnlineBackup(ctx, destination)
	closeErr := store.Close()
	if backupErr != nil || closeErr != nil {
		return errors.Join(backupErr, closeErr)
	}
	return nil
}

func (f *Factory) backupSources(databaseSnapshot string, snapshot sqliteevent.SnapshotInfo) ([]archiveSource, vaultbackup.FileRecord, error) {
	databaseRecord, err := fileRecord(databaseEntryName, databaseSnapshot, maxPlaintextBackupBytes)
	if err != nil {
		return nil, vaultbackup.FileRecord{}, fmt.Errorf("inspect Vault database backup source: %w", err)
	}
	sources := []archiveSource{{record: databaseRecord, path: databaseSnapshot}}
	if len(snapshot.Artifacts) > maxArtifactCount {
		return nil, vaultbackup.FileRecord{}, vaultbackup.ErrCorrupt
	}
	seen := map[string]struct{}{}
	var totalBytes = databaseRecord.SizeBytes
	for _, artifact := range snapshot.Artifacts {
		if !validHash(artifact.CiphertextHash) || artifact.StorageName != artifact.CiphertextHash+".blob" {
			return nil, vaultbackup.FileRecord{}, vaultbackup.ErrCorrupt
		}
		entryName := blobEntryPrefix + artifact.StorageName
		if _, exists := seen[entryName]; exists {
			return nil, vaultbackup.FileRecord{}, vaultbackup.ErrCorrupt
		}
		seen[entryName] = struct{}{}
		sourcePath, err := f.path(snapshot.VaultID)
		if err != nil {
			return nil, vaultbackup.FileRecord{}, err
		}
		sourcePath = filepath.Join(sourcePath+".blobs", artifact.StorageName)
		record, err := fileRecord(entryName, sourcePath, maxPlaintextBackupBytes-totalBytes)
		if err != nil {
			return nil, vaultbackup.FileRecord{}, fmt.Errorf("inspect Vault artifact backup source: %w", err)
		}
		if record.SHA256 != artifact.CiphertextHash {
			return nil, vaultbackup.FileRecord{}, vaultbackup.ErrCorrupt
		}
		totalBytes += record.SizeBytes
		if totalBytes > maxPlaintextBackupBytes {
			return nil, vaultbackup.FileRecord{}, vaultbackup.ErrCorrupt
		}
		sources = append(sources, archiveSource{record: record, path: sourcePath})
	}
	sort.Slice(sources[1:], func(left, right int) bool {
		return sources[left+1].record.Name < sources[right+1].record.Name
	})
	return sources, databaseRecord, nil
}

func writeEncryptedArchive(destination *os.File, keyID string, key []byte, createdAt time.Time, manifest []byte, sources []archiveSource) error {
	stream, err := backupstream.NewWriter(destination, keyID, key)
	if err != nil {
		return fmt.Errorf("initialize encrypted Vault backup: %w", err)
	}
	archive := tar.NewWriter(stream)
	writeErr := writeTarEntry(archive, manifestEntryName, createdAt, int64(len(manifest)), bytesReader(manifest))
	if writeErr == nil {
		for _, source := range sources {
			file, err := os.Open(source.path)
			if err != nil {
				writeErr = fmt.Errorf("open Vault backup source %s: %w", source.record.Name, err)
				break
			}
			writeErr = writeTarEntry(archive, source.record.Name, createdAt, source.record.SizeBytes, file)
			closeErr := file.Close()
			if writeErr == nil && closeErr != nil {
				writeErr = fmt.Errorf("close Vault backup source %s: %w", source.record.Name, closeErr)
			}
			if writeErr != nil {
				break
			}
		}
	}
	archiveErr := archive.Close()
	streamErr := stream.Close()
	if writeErr != nil || archiveErr != nil || streamErr != nil {
		return errors.Join(writeErr, archiveErr, streamErr)
	}
	return nil
}

func writeTarEntry(archive *tar.Writer, name string, createdAt time.Time, size int64, source io.Reader) error {
	header := &tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: createdAt, Typeflag: tar.TypeReg, Format: tar.FormatUSTAR}
	if err := archive.WriteHeader(header); err != nil {
		return fmt.Errorf("write Vault backup entry header %s: %w", name, err)
	}
	written, err := io.CopyN(archive, source, size)
	if err != nil {
		return fmt.Errorf("write Vault backup entry %s after %d bytes: %w", name, written, err)
	}
	var trailing [1]byte
	if count, readErr := source.Read(trailing[:]); count != 0 || readErr != io.EOF {
		return fmt.Errorf("Vault backup source %s changed while reading", name)
	}
	return nil
}

func (f *Factory) preflightBackupFile(ctx context.Context, input vaultbackup.PreflightInput, requireExtension bool) (vaultbackup.Preflight, error) {
	if requireExtension && !strings.EqualFold(filepath.Ext(input.Source), vaultbackup.FileExtension) {
		return vaultbackup.Preflight{}, vaultbackup.ErrUnsafePath
	}
	info, err := os.Lstat(input.Source)
	if err != nil {
		return vaultbackup.Preflight{}, fmt.Errorf("inspect encrypted Vault backup: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > maxEncryptedBackupBytes {
		return vaultbackup.Preflight{}, vaultbackup.ErrCorrupt
	}
	ciphertextHash, ciphertextBytes, err := hashFile(input.Source, maxEncryptedBackupBytes)
	if err != nil {
		return vaultbackup.Preflight{}, fmt.Errorf("hash encrypted Vault backup: %w", err)
	}
	file, err := os.Open(input.Source)
	if err != nil {
		return vaultbackup.Preflight{}, fmt.Errorf("open encrypted Vault backup: %w", err)
	}
	defer file.Close()
	stream, err := backupstream.NewReader(file, input.KeyID, input.Key)
	if err != nil {
		return vaultbackup.Preflight{}, errors.Join(vaultbackup.ErrCorrupt, err)
	}
	defer stream.Close()
	archive := tar.NewReader(stream)
	manifest, err := readManifest(archive)
	if err != nil {
		return vaultbackup.Preflight{}, err
	}
	if err := validateManifest(manifest, input.VaultID, input.KeyID, input.ApplicationVersion); err != nil {
		return vaultbackup.Preflight{}, err
	}

	stagingRoot, err := os.MkdirTemp(f.root, ".backup-preflight-")
	if err != nil {
		return vaultbackup.Preflight{}, fmt.Errorf("create Vault backup preflight directory: %w", err)
	}
	defer os.RemoveAll(stagingRoot)
	expected := make(map[string]vaultbackup.FileRecord, len(manifest.Artifacts)+1)
	expected[manifest.Database.Name] = manifest.Database
	for _, artifact := range manifest.Artifacts {
		expected[artifact.Name] = artifact
	}
	seen := make(map[string]struct{}, len(expected))
	for {
		header, err := archive.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			return vaultbackup.Preflight{}, errors.Join(vaultbackup.ErrCorrupt, fmt.Errorf("read Vault backup archive: %w", err))
		}
		record, exists := expected[header.Name]
		if !exists || header.Typeflag != tar.TypeReg || header.Size != record.SizeBytes {
			return vaultbackup.Preflight{}, vaultbackup.ErrCorrupt
		}
		if _, duplicate := seen[header.Name]; duplicate {
			return vaultbackup.Preflight{}, vaultbackup.ErrCorrupt
		}
		seen[header.Name] = struct{}{}
		destination, err := extractedPath(stagingRoot, header.Name)
		if err != nil {
			return vaultbackup.Preflight{}, err
		}
		if err := writeExtractedFile(destination, archive, record); err != nil {
			return vaultbackup.Preflight{}, err
		}
	}
	if len(seen) != len(expected) {
		return vaultbackup.Preflight{}, vaultbackup.ErrCorrupt
	}
	if _, err := io.Copy(io.Discard, stream); err != nil {
		return vaultbackup.Preflight{}, errors.Join(vaultbackup.ErrCorrupt, err)
	}

	databasePath := filepath.Join(stagingRoot, databaseEntryName)
	before, err := sqliteevent.InspectSnapshot(ctx, databasePath)
	if err != nil {
		return vaultbackup.Preflight{}, errors.Join(vaultbackup.ErrMigrationPreflight, err)
	}
	if before.VaultID != manifest.VaultID || before.SchemaVersion != manifest.SourceSchemaVersion || len(before.Artifacts) != len(manifest.Artifacts) {
		return vaultbackup.Preflight{}, vaultbackup.ErrCorrupt
	}
	sealer, err := envelope.NewSealer(input.KeyID, input.Key)
	if err != nil {
		return vaultbackup.Preflight{}, err
	}
	store, err := sqliteevent.Open(databasePath, sealer)
	if err != nil {
		sealer.Destroy()
		return vaultbackup.Preflight{}, errors.Join(vaultbackup.ErrMigrationPreflight, err)
	}
	record, recordErr := store.GetVault(ctx, input.VaultID)
	report, integrityErr := store.ValidateIntegrity(ctx, input.VaultID)
	checkpointErr := store.Checkpoint(ctx)
	closeErr := store.Close()
	sealer.Destroy()
	if recordErr != nil || record.ID != input.VaultID || record.Status != vault.StatusActive || integrityErr != nil || checkpointErr != nil || closeErr != nil {
		return vaultbackup.Preflight{}, errors.Join(vaultbackup.ErrMigrationPreflight, recordErr, integrityErr, checkpointErr, closeErr)
	}
	after, err := sqliteevent.InspectSnapshot(ctx, databasePath)
	if err != nil {
		return vaultbackup.Preflight{}, errors.Join(vaultbackup.ErrMigrationPreflight, err)
	}
	if after.SchemaVersion != sqliteevent.CurrentSchemaVersion() || after.VaultID != input.VaultID || report.ArtifactCount != len(manifest.Artifacts) {
		return vaultbackup.Preflight{}, vaultbackup.ErrMigrationPreflight
	}
	return vaultbackup.Preflight{
		Schema:                   vaultbackup.PreflightSchema,
		BackupID:                 manifest.BackupID,
		VaultID:                  manifest.VaultID,
		Path:                     input.Source,
		SourceApplicationVersion: manifest.SourceApplicationVersion,
		TargetApplicationVersion: input.ApplicationVersion,
		SourceSchemaVersion:      before.SchemaVersion,
		TargetSchemaVersion:      after.SchemaVersion,
		MigrationRequired:        before.SchemaVersion != after.SchemaVersion,
		CreatedAt:                manifest.CreatedAt,
		VerifiedAt:               f.now().UTC(),
		EncryptedSizeBytes:       ciphertextBytes,
		CiphertextSHA256:         ciphertextHash,
		DatabaseSizeBytes:        manifest.Database.SizeBytes,
		ArtifactCount:            report.ArtifactCount,
		EventCount:               report.EventCount,
	}, nil
}

func readManifest(archive *tar.Reader) (vaultbackup.Manifest, error) {
	header, err := archive.Next()
	if err != nil {
		return vaultbackup.Manifest{}, errors.Join(vaultbackup.ErrCorrupt, err)
	}
	if header.Name != manifestEntryName || header.Typeflag != tar.TypeReg || header.Size <= 0 || header.Size > maxManifestBytes {
		return vaultbackup.Manifest{}, vaultbackup.ErrCorrupt
	}
	payload := make([]byte, header.Size)
	if _, err := io.ReadFull(archive, payload); err != nil {
		return vaultbackup.Manifest{}, errors.Join(vaultbackup.ErrCorrupt, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(payload))
	decoder.DisallowUnknownFields()
	var manifest vaultbackup.Manifest
	if err := decoder.Decode(&manifest); err != nil {
		clear(payload)
		return vaultbackup.Manifest{}, errors.Join(vaultbackup.ErrCorrupt, err)
	}
	clear(payload)
	if decoder.Decode(&struct{}{}) != io.EOF {
		return vaultbackup.Manifest{}, vaultbackup.ErrCorrupt
	}
	return manifest, nil
}

func validateManifest(manifest vaultbackup.Manifest, vaultID, keyID, applicationVersion string) error {
	if manifest.Schema != vaultbackup.ManifestSchema || !id.IsUUIDv7(manifest.BackupID) || manifest.VaultID != vaultID || manifest.KeyID != keyID || !validSemanticVersion(manifest.SourceApplicationVersion) || !validSemanticVersion(manifest.MinimumRestoreVersion) || manifest.SourceSchemaVersion < 1 || manifest.SourceSchemaVersion > sqliteevent.CurrentSchemaVersion() || manifest.CreatedAt.IsZero() || manifest.CreatedAt.Location() != time.UTC || manifest.Database.Name != databaseEntryName || !validFileRecord(manifest.Database) || len(manifest.Artifacts) > maxArtifactCount {
		if manifest.VaultID != "" && manifest.VaultID != vaultID {
			return vaultbackup.ErrWrongVault
		}
		return vaultbackup.ErrCorrupt
	}
	if compareSemanticVersion(applicationVersion, manifest.MinimumRestoreVersion) < 0 {
		return vaultbackup.ErrUnsupportedFormat
	}
	seen := map[string]struct{}{databaseEntryName: {}}
	previous := ""
	var total = manifest.Database.SizeBytes
	for _, artifact := range manifest.Artifacts {
		if !validFileRecord(artifact) || !strings.HasPrefix(artifact.Name, blobEntryPrefix) || filepath.Base(artifact.Name) != strings.TrimPrefix(artifact.Name, blobEntryPrefix) || !strings.HasSuffix(artifact.Name, ".blob") || !validHash(strings.TrimSuffix(filepath.Base(artifact.Name), ".blob")) || artifact.SHA256 != strings.TrimSuffix(filepath.Base(artifact.Name), ".blob") || previous != "" && artifact.Name <= previous {
			return vaultbackup.ErrCorrupt
		}
		if _, duplicate := seen[artifact.Name]; duplicate {
			return vaultbackup.ErrCorrupt
		}
		seen[artifact.Name] = struct{}{}
		previous = artifact.Name
		total += artifact.SizeBytes
		if total > maxPlaintextBackupBytes {
			return vaultbackup.ErrCorrupt
		}
	}
	return nil
}

func extractedPath(root, name string) (string, error) {
	if name == databaseEntryName {
		return filepath.Join(root, databaseEntryName), nil
	}
	if !strings.HasPrefix(name, blobEntryPrefix) {
		return "", vaultbackup.ErrCorrupt
	}
	base := strings.TrimPrefix(name, blobEntryPrefix)
	if base == "" || filepath.Base(base) != base || strings.ContainsAny(base, `/\`) {
		return "", vaultbackup.ErrCorrupt
	}
	directory := filepath.Join(root, databaseEntryName+".blobs")
	if err := os.MkdirAll(directory, 0o700); err != nil {
		return "", fmt.Errorf("create Vault backup artifact staging directory: %w", err)
	}
	return filepath.Join(directory, base), nil
}

func writeExtractedFile(destination string, source io.Reader, expected vaultbackup.FileRecord) error {
	file, err := os.OpenFile(destination, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		return fmt.Errorf("create Vault backup preflight file: %w", err)
	}
	remove := true
	defer func() {
		_ = file.Close()
		if remove {
			_ = os.Remove(destination)
		}
	}()
	hash := sha256.New()
	written, err := io.CopyN(io.MultiWriter(file, hash), source, expected.SizeBytes)
	if err != nil || written != expected.SizeBytes {
		return errors.Join(vaultbackup.ErrCorrupt, err)
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return vaultbackup.ErrCorrupt
	}
	if err := file.Sync(); err != nil {
		return fmt.Errorf("sync Vault backup preflight file: %w", err)
	}
	if err := file.Close(); err != nil {
		return fmt.Errorf("close Vault backup preflight file: %w", err)
	}
	remove = false
	return nil
}

func fileRecord(name, path string, limit int64) (vaultbackup.FileRecord, error) {
	if limit <= 0 {
		return vaultbackup.FileRecord{}, vaultbackup.ErrCorrupt
	}
	hash, size, err := hashFile(path, limit)
	if err != nil {
		return vaultbackup.FileRecord{}, err
	}
	return vaultbackup.FileRecord{Name: name, SizeBytes: size, SHA256: hash}, nil
}

func hashFile(path string, limit int64) (string, int64, error) {
	info, err := os.Lstat(path)
	if err != nil {
		return "", 0, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || info.Size() <= 0 || info.Size() > limit {
		return "", 0, vaultbackup.ErrCorrupt
	}
	file, err := os.Open(path)
	if err != nil {
		return "", 0, err
	}
	defer file.Close()
	hash := sha256.New()
	written, err := io.Copy(hash, io.LimitReader(file, limit+1))
	if err != nil {
		return "", 0, err
	}
	if written != info.Size() || written > limit {
		return "", 0, vaultbackup.ErrCorrupt
	}
	return hex.EncodeToString(hash.Sum(nil)), written, nil
}

func canonicalBackupDestination(value string) (string, error) {
	if strings.TrimSpace(value) == "" || strings.IndexByte(value, 0) >= 0 || len(value) > 32767 || !filepath.IsAbs(value) || !strings.EqualFold(filepath.Ext(value), vaultbackup.FileExtension) {
		return "", vaultbackup.ErrUnsafePath
	}
	base := filepath.Base(filepath.Clean(value))
	if base == "." || base == string(filepath.Separator) || strings.HasSuffix(base, ".") || strings.HasSuffix(base, " ") {
		return "", vaultbackup.ErrUnsafePath
	}
	parent := filepath.Dir(filepath.Clean(value))
	info, err := os.Lstat(parent)
	if err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
		return "", vaultbackup.ErrUnsafePath
	}
	resolved, err := filepath.EvalSymlinks(parent)
	if err != nil {
		return "", vaultbackup.ErrUnsafePath
	}
	return filepath.Join(filepath.Clean(resolved), base), nil
}

func canonicalExistingBackup(value string) (string, error) {
	canonical, err := canonicalBackupDestination(value)
	if err != nil {
		return "", err
	}
	info, err := os.Lstat(canonical)
	if err != nil || !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return "", vaultbackup.ErrUnsafePath
	}
	return canonical, nil
}

func validFileRecord(record vaultbackup.FileRecord) bool {
	return record.Name != "" && record.SizeBytes > 0 && record.SizeBytes <= maxPlaintextBackupBytes && validHash(record.SHA256)
}

func validHash(value string) bool {
	if len(value) != sha256.Size*2 || strings.ToLower(value) != value {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size
}

func validSemanticVersion(value string) bool {
	return len(value) <= maxApplicationVersion && semanticVersionPattern.MatchString(value)
}

func compareSemanticVersion(left, right string) int {
	leftParts := semanticVersionPattern.FindStringSubmatch(left)
	rightParts := semanticVersionPattern.FindStringSubmatch(right)
	for index := 1; index <= 3; index++ {
		leftValue, _ := strconv.ParseUint(leftParts[index], 10, 64)
		rightValue, _ := strconv.ParseUint(rightParts[index], 10, 64)
		if leftValue < rightValue {
			return -1
		}
		if leftValue > rightValue {
			return 1
		}
	}
	return 0
}

func bytesReader(payload []byte) io.Reader {
	return bytes.NewReader(payload)
}
