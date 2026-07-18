package vaultbackup

import (
	"context"
	"errors"
	"time"
)

const (
	FileExtension   = ".talos-backup"
	ManifestSchema  = "talos.vault-backup-manifest/1"
	ReceiptSchema   = "talos.vault-backup-receipt/1"
	PreflightSchema = "talos.vault-backup-preflight/1"
	RestoreSchema   = "talos.vault-backup-restore/1"
)

var (
	ErrInvalidRequest     = errors.New("invalid Vault backup request")
	ErrUnsafePath         = errors.New("Vault backup path is unsafe")
	ErrAlreadyExists      = errors.New("Vault backup destination already exists")
	ErrCorrupt            = errors.New("Vault backup is corrupt")
	ErrWrongVault         = errors.New("Vault backup belongs to another Vault")
	ErrUnsupportedFormat  = errors.New("Vault backup format is unsupported")
	ErrMigrationPreflight = errors.New("Vault backup migration preflight failed")
	ErrRestorePending     = errors.New("Vault restore is already pending")
	ErrRestoreIncomplete  = errors.New("Vault restore could not establish a valid generation")
	ErrRestoreRolledBack  = errors.New("Vault restore failed and the original generation was recovered")
)

type CreateInput struct {
	VaultID            string
	KeyID              string
	Key                []byte
	Destination        string
	ApplicationVersion string
	CreatedAt          time.Time
}

type PreflightInput struct {
	VaultID            string
	KeyID              string
	Key                []byte
	Source             string
	ApplicationVersion string
}

type StageRestoreInput struct {
	VaultID                  string
	KeyID                    string
	Key                      []byte
	Source                   string
	ApplicationVersion       string
	ExpectedBackupID         string
	ExpectedCiphertextSHA256 string
}

type ReconcileRestoreInput struct {
	VaultID            string
	KeyID              string
	Key                []byte
	ApplicationVersion string
}

type FileRecord struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type Manifest struct {
	Schema                   string       `json:"schema"`
	BackupID                 string       `json:"backup_id"`
	VaultID                  string       `json:"vault_id"`
	KeyID                    string       `json:"key_id"`
	SourceApplicationVersion string       `json:"source_application_version"`
	MinimumRestoreVersion    string       `json:"minimum_restore_version"`
	SourceSchemaVersion      int          `json:"source_schema_version"`
	CreatedAt                time.Time    `json:"created_at"`
	Database                 FileRecord   `json:"database"`
	Artifacts                []FileRecord `json:"artifacts"`
}

type Receipt struct {
	Schema                   string    `json:"schema"`
	BackupID                 string    `json:"backup_id"`
	VaultID                  string    `json:"vault_id"`
	Path                     string    `json:"path"`
	SourceApplicationVersion string    `json:"source_application_version"`
	SourceSchemaVersion      int       `json:"source_schema_version"`
	CreatedAt                time.Time `json:"created_at"`
	EncryptedSizeBytes       int64     `json:"encrypted_size_bytes"`
	CiphertextSHA256         string    `json:"ciphertext_sha256"`
	DatabaseSizeBytes        int64     `json:"database_size_bytes"`
	ArtifactCount            int       `json:"artifact_count"`
	EventCount               int       `json:"event_count"`
}

type Preflight struct {
	Schema                   string    `json:"schema"`
	BackupID                 string    `json:"backup_id"`
	VaultID                  string    `json:"vault_id"`
	Path                     string    `json:"path"`
	SourceApplicationVersion string    `json:"source_application_version"`
	MinimumRestoreVersion    string    `json:"minimum_restore_version"`
	TargetApplicationVersion string    `json:"target_application_version"`
	SourceSchemaVersion      int       `json:"source_schema_version"`
	TargetSchemaVersion      int       `json:"target_schema_version"`
	MigrationRequired        bool      `json:"migration_required"`
	CreatedAt                time.Time `json:"created_at"`
	VerifiedAt               time.Time `json:"verified_at"`
	EncryptedSizeBytes       int64     `json:"encrypted_size_bytes"`
	CiphertextSHA256         string    `json:"ciphertext_sha256"`
	DatabaseSizeBytes        int64     `json:"database_size_bytes"`
	ArtifactCount            int       `json:"artifact_count"`
	EventCount               int       `json:"event_count"`
}

type RestoreState string

const (
	RestoreStateRestored   RestoreState = "restored"
	RestoreStateRolledBack RestoreState = "rolled_back"
)

type Restore struct {
	Schema                   string       `json:"schema"`
	State                    RestoreState `json:"state"`
	BackupID                 string       `json:"backup_id"`
	VaultID                  string       `json:"vault_id"`
	CiphertextSHA256         string       `json:"ciphertext_sha256"`
	SourceApplicationVersion string       `json:"source_application_version"`
	TargetApplicationVersion string       `json:"target_application_version"`
	SourceSchemaVersion      int          `json:"source_schema_version"`
	TargetSchemaVersion      int          `json:"target_schema_version"`
	RestoredAt               time.Time    `json:"restored_at"`
	ArtifactCount            int          `json:"artifact_count"`
	EventCount               int          `json:"event_count"`
}

type Store interface {
	CreateBackup(context.Context, CreateInput) (Receipt, error)
	PreflightBackup(context.Context, PreflightInput) (Preflight, error)
}

type Restorer interface {
	StageRestore(context.Context, StageRestoreInput) (Preflight, error)
	ReconcileRestore(context.Context, ReconcileRestoreInput) (Restore, error)
	FinalizeRestore(context.Context, string) error
	CleanupInactiveRestore(context.Context, string) error
}
