package releaseupdate

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/id"
)

const (
	ManifestSchema    = "talos.update-manifest/1"
	PreparationSchema = "talos.update-preparation/1"
)

var (
	ErrInvalidManifest    = errors.New("update manifest is invalid")
	ErrInvalidPreparation = errors.New("update preparation is invalid")
)

type Channel string

const (
	ChannelAlpha  Channel = "alpha"
	ChannelBeta   Channel = "beta"
	ChannelStable Channel = "stable"
)

type Target struct {
	OS           string `json:"os"`
	Architecture string `json:"architecture"`
}

type Artifact struct {
	Name      string `json:"name"`
	SizeBytes int64  `json:"size_bytes"`
	SHA256    string `json:"sha256"`
}

type Manifest struct {
	Schema                string    `json:"schema"`
	ReleaseID             string    `json:"release_id"`
	Channel               Channel   `json:"channel"`
	Version               string    `json:"version"`
	MinimumCurrentVersion string    `json:"minimum_current_version"`
	PublishedAt           time.Time `json:"published_at"`
	ExpiresAt             time.Time `json:"expires_at"`
	Target                Target    `json:"target"`
	Artifact              Artifact  `json:"artifact"`
}

type VerifiedRelease struct {
	Manifest       Manifest
	ManifestSHA256 string
	SigningKeyID   string
	VerifiedAt     time.Time
}

type Preparation struct {
	Schema                 string    `json:"schema"`
	PreparationID          string    `json:"preparation_id"`
	ReleaseID              string    `json:"release_id"`
	Channel                Channel   `json:"channel"`
	TargetVersion          string    `json:"target_version"`
	CurrentVersion         string    `json:"current_version"`
	TargetOS               string    `json:"target_os"`
	TargetArchitecture     string    `json:"target_architecture"`
	ManifestSHA256         string    `json:"manifest_sha256"`
	ArtifactName           string    `json:"artifact_name"`
	ArtifactSHA256         string    `json:"artifact_sha256"`
	SigningKeyID           string    `json:"signing_key_id"`
	VaultID                string    `json:"vault_id"`
	VaultRevision          int       `json:"vault_revision"`
	BackupID               string    `json:"backup_id"`
	BackupCiphertextSHA256 string    `json:"backup_ciphertext_sha256"`
	BackupSourceSchema     int       `json:"backup_source_schema"`
	BackupTargetSchema     int       `json:"backup_target_schema"`
	PreparedAt             time.Time `json:"prepared_at"`
	ExpiresAt              time.Time `json:"expires_at"`
}

func (channel Channel) Valid() bool {
	return channel == ChannelAlpha || channel == ChannelBeta || channel == ChannelStable
}

func ValidateManifest(manifest Manifest) error {
	if manifest.Schema != ManifestSchema || !id.IsUUIDv7(manifest.ReleaseID) || !manifest.Channel.Valid() {
		return ErrInvalidManifest
	}
	if _, err := ParseVersion(manifest.Version); err != nil {
		return fmt.Errorf("%w: target version", ErrInvalidManifest)
	}
	if _, err := ParseVersion(manifest.MinimumCurrentVersion); err != nil {
		return fmt.Errorf("%w: minimum current version", ErrInvalidManifest)
	}
	if manifest.PublishedAt.IsZero() || manifest.ExpiresAt.IsZero() || !manifest.ExpiresAt.After(manifest.PublishedAt) {
		return fmt.Errorf("%w: validity window", ErrInvalidManifest)
	}
	if !validToken(manifest.Target.OS, 32) || !validToken(manifest.Target.Architecture, 32) {
		return fmt.Errorf("%w: target", ErrInvalidManifest)
	}
	if !validArtifactName(manifest.Artifact.Name) || manifest.Artifact.SizeBytes < 1 || !validSHA256(manifest.Artifact.SHA256) {
		return fmt.Errorf("%w: artifact", ErrInvalidManifest)
	}
	return nil
}

func ValidatePreparation(preparation Preparation) error {
	if preparation.Schema != PreparationSchema || !id.IsUUIDv7(preparation.PreparationID) || !id.IsUUIDv7(preparation.ReleaseID) || !preparation.Channel.Valid() {
		return ErrInvalidPreparation
	}
	if _, err := ParseVersion(preparation.TargetVersion); err != nil {
		return ErrInvalidPreparation
	}
	if _, err := ParseVersion(preparation.CurrentVersion); err != nil {
		return ErrInvalidPreparation
	}
	if !validToken(preparation.TargetOS, 32) || !validToken(preparation.TargetArchitecture, 32) || !validArtifactName(preparation.ArtifactName) {
		return ErrInvalidPreparation
	}
	if !validSHA256(preparation.ManifestSHA256) || !validSHA256(preparation.ArtifactSHA256) || !validSHA256(preparation.SigningKeyID) || !validSHA256(preparation.BackupCiphertextSHA256) {
		return ErrInvalidPreparation
	}
	if !id.IsUUIDv7(preparation.VaultID) || preparation.VaultRevision < 1 || !id.IsUUIDv7(preparation.BackupID) || preparation.BackupSourceSchema < 1 || preparation.BackupTargetSchema < 1 {
		return ErrInvalidPreparation
	}
	if preparation.PreparedAt.IsZero() || preparation.ExpiresAt.IsZero() || !preparation.ExpiresAt.After(preparation.PreparedAt) {
		return ErrInvalidPreparation
	}
	return nil
}

type Version struct {
	Major uint64
	Minor uint64
	Patch uint64
}

func ParseVersion(value string) (Version, error) {
	parts := strings.Split(value, ".")
	if len(parts) != 3 {
		return Version{}, ErrInvalidManifest
	}
	values := [3]uint64{}
	for index, part := range parts {
		if part == "" || len(part) > 1 && part[0] == '0' {
			return Version{}, ErrInvalidManifest
		}
		parsed, err := strconv.ParseUint(part, 10, 63)
		if err != nil {
			return Version{}, ErrInvalidManifest
		}
		values[index] = parsed
	}
	return Version{Major: values[0], Minor: values[1], Patch: values[2]}, nil
}

func CompareVersions(left, right string) (int, error) {
	a, err := ParseVersion(left)
	if err != nil {
		return 0, err
	}
	b, err := ParseVersion(right)
	if err != nil {
		return 0, err
	}
	leftValues := [...]uint64{a.Major, a.Minor, a.Patch}
	rightValues := [...]uint64{b.Major, b.Minor, b.Patch}
	for index := range leftValues {
		if leftValues[index] < rightValues[index] {
			return -1, nil
		}
		if leftValues[index] > rightValues[index] {
			return 1, nil
		}
	}
	return 0, nil
}

func validToken(value string, limit int) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for _, character := range value {
		if character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-' || character == '_' {
			continue
		}
		return false
	}
	return true
}

func validArtifactName(value string) bool {
	if value == "" || len(value) > 128 || value == "." || value == ".." || strings.ContainsAny(value, `/\\`) {
		return false
	}
	for _, character := range value {
		if character < 0x20 || character == 0x7f {
			return false
		}
	}
	return true
}

func validSHA256(value string) bool {
	if len(value) != 64 {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}
