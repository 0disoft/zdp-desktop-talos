package signedupdate

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/releaseupdate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/updateverify"
)

const (
	maxManifestBytes  = 64 * 1024
	maxSignatureBytes = 256
	maxArtifactBytes  = int64(2 * 1024 * 1024 * 1024)
	clockSkew         = 5 * time.Minute
)

type Verifier struct {
	publicKey ed25519.PublicKey
	now       func() time.Time
}

func New(publicKey ed25519.PublicKey, now func() time.Time) (*Verifier, error) {
	if len(publicKey) != ed25519.PublicKeySize || now == nil {
		return nil, updateverify.ErrInvalidRequest
	}
	return &Verifier{publicKey: append(ed25519.PublicKey(nil), publicKey...), now: now}, nil
}

func (v *Verifier) VerifyFiles(ctx context.Context, input updateverify.VerifyFilesInput) (releaseupdate.VerifiedRelease, error) {
	if v == nil || ctx == nil || strings.TrimSpace(input.ManifestPath) == "" || strings.TrimSpace(input.SignaturePath) == "" || strings.TrimSpace(input.ArtifactPath) == "" || !input.ExpectedChannel.Valid() || strings.TrimSpace(input.ExpectedOS) == "" || strings.TrimSpace(input.ExpectedArchitecture) == "" {
		return releaseupdate.VerifiedRelease{}, updateverify.ErrInvalidRequest
	}
	if _, err := releaseupdate.ParseVersion(input.CurrentVersion); err != nil {
		return releaseupdate.VerifiedRelease{}, updateverify.ErrInvalidRequest
	}
	manifestBytes, err := readRegularBounded(ctx, input.ManifestPath, maxManifestBytes)
	if err != nil {
		return releaseupdate.VerifiedRelease{}, fmt.Errorf("read update manifest: %w", err)
	}
	signatureBytes, err := readRegularBounded(ctx, input.SignaturePath, maxSignatureBytes)
	if err != nil {
		return releaseupdate.VerifiedRelease{}, fmt.Errorf("read update signature: %w", err)
	}
	signature, err := base64.StdEncoding.DecodeString(strings.TrimSpace(string(signatureBytes)))
	if err != nil || len(signature) != ed25519.SignatureSize || !ed25519.Verify(v.publicKey, manifestBytes, signature) {
		return releaseupdate.VerifiedRelease{}, updateverify.ErrSignatureInvalid
	}

	manifest, err := decodeManifest(manifestBytes)
	if err != nil {
		return releaseupdate.VerifiedRelease{}, err
	}
	now := v.now().UTC()
	if manifest.PublishedAt.After(now.Add(clockSkew)) || !manifest.ExpiresAt.After(now) {
		return releaseupdate.VerifiedRelease{}, updateverify.ErrManifestExpired
	}
	if manifest.Channel != input.ExpectedChannel {
		return releaseupdate.VerifiedRelease{}, updateverify.ErrChannelMismatch
	}
	if manifest.Target.OS != input.ExpectedOS || manifest.Target.Architecture != input.ExpectedArchitecture {
		return releaseupdate.VerifiedRelease{}, updateverify.ErrTargetMismatch
	}
	minimumComparison, _ := releaseupdate.CompareVersions(input.CurrentVersion, manifest.MinimumCurrentVersion)
	targetComparison, _ := releaseupdate.CompareVersions(manifest.Version, input.CurrentVersion)
	if minimumComparison < 0 || targetComparison <= 0 {
		return releaseupdate.VerifiedRelease{}, updateverify.ErrVersionRejected
	}
	if err := verifyArtifact(ctx, input.ArtifactPath, manifest.Artifact); err != nil {
		return releaseupdate.VerifiedRelease{}, err
	}

	manifestHash := sha256.Sum256(manifestBytes)
	keyHash := sha256.Sum256(v.publicKey)
	return releaseupdate.VerifiedRelease{
		Manifest:       manifest,
		ManifestSHA256: hex.EncodeToString(manifestHash[:]),
		SigningKeyID:   hex.EncodeToString(keyHash[:]),
		VerifiedAt:     now,
	}, nil
}

func decodeManifest(encoded []byte) (releaseupdate.Manifest, error) {
	decoder := json.NewDecoder(bytes.NewReader(encoded))
	decoder.DisallowUnknownFields()
	var manifest releaseupdate.Manifest
	if err := decoder.Decode(&manifest); err != nil {
		return releaseupdate.Manifest{}, fmt.Errorf("%w: %v", updateverify.ErrManifestInvalid, err)
	}
	var extra any
	if err := decoder.Decode(&extra); !errors.Is(err, io.EOF) {
		return releaseupdate.Manifest{}, updateverify.ErrManifestInvalid
	}
	if err := releaseupdate.ValidateManifest(manifest); err != nil {
		return releaseupdate.Manifest{}, fmt.Errorf("%w: %v", updateverify.ErrManifestInvalid, err)
	}
	return manifest, nil
}

func readRegularBounded(ctx context.Context, path string, limit int64) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	info, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 {
		return nil, updateverify.ErrUnsafeFile
	}
	if info.Size() < 1 || info.Size() > limit {
		return nil, updateverify.ErrManifestTooLarge
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	defer file.Close()
	encoded, err := io.ReadAll(io.LimitReader(file, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(encoded)) > limit {
		return nil, updateverify.ErrManifestTooLarge
	}
	after, err := file.Stat()
	if err != nil || after.Size() != info.Size() || !os.SameFile(info, after) {
		return nil, updateverify.ErrArtifactChanged
	}
	return encoded, nil
}

func verifyArtifact(ctx context.Context, path string, expected releaseupdate.Artifact) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("inspect update artifact: %w", err)
	}
	if !info.Mode().IsRegular() || info.Mode()&os.ModeSymlink != 0 || filepath.Base(filepath.Clean(path)) != expected.Name {
		return updateverify.ErrUnsafeFile
	}
	if info.Size() != expected.SizeBytes || info.Size() > maxArtifactBytes {
		return updateverify.ErrArtifactMismatch
	}
	file, err := os.Open(path)
	if err != nil {
		return fmt.Errorf("open update artifact: %w", err)
	}
	defer file.Close()
	hash := sha256.New()
	buffer := make([]byte, 1024*1024)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		read, readErr := file.Read(buffer)
		if read > 0 {
			_, _ = hash.Write(buffer[:read])
		}
		if errors.Is(readErr, io.EOF) {
			break
		}
		if readErr != nil {
			return fmt.Errorf("hash update artifact: %w", readErr)
		}
	}
	after, err := file.Stat()
	if err != nil || after.Size() != info.Size() || !os.SameFile(info, after) {
		return updateverify.ErrArtifactChanged
	}
	if hex.EncodeToString(hash.Sum(nil)) != expected.SHA256 {
		return updateverify.ErrArtifactMismatch
	}
	return nil
}
