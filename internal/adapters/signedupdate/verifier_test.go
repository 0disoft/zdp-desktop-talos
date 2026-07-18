package signedupdate

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/releaseupdate"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/updateverify"
)

func TestVerifierBindsSignatureTargetVersionAndArtifact(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	artifactPath := filepath.Join(root, "talos-setup.exe")
	artifact := []byte("signed installer fixture")
	if err := os.WriteFile(artifactPath, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	artifactHash := sha256.Sum256(artifact)
	releaseID, err := id.UUIDv7(now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := releaseupdate.Manifest{
		Schema: releaseupdate.ManifestSchema, ReleaseID: releaseID, Channel: releaseupdate.ChannelBeta,
		Version: "0.33.0", MinimumCurrentVersion: "0.32.0", PublishedAt: now.Add(-time.Hour), ExpiresAt: now.Add(time.Hour),
		Target:   releaseupdate.Target{OS: "windows", Architecture: "amd64"},
		Artifact: releaseupdate.Artifact{Name: filepath.Base(artifactPath), SizeBytes: int64(len(artifact)), SHA256: hex.EncodeToString(artifactHash[:])},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	signaturePath := filepath.Join(root, "manifest.sig")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signaturePath, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestBytes))), 0o600); err != nil {
		t.Fatal(err)
	}
	verifier, err := New(publicKey, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	verified, err := verifier.VerifyFiles(context.Background(), updateverify.VerifyFilesInput{
		ManifestPath: manifestPath, SignaturePath: signaturePath, ArtifactPath: artifactPath,
		ExpectedChannel: releaseupdate.ChannelBeta, ExpectedOS: "windows", ExpectedArchitecture: "amd64", CurrentVersion: "0.32.0",
	})
	if err != nil {
		t.Fatal(err)
	}
	if verified.Manifest.ReleaseID != releaseID || verified.ManifestSHA256 == "" || verified.SigningKeyID == "" || !verified.VerifiedAt.Equal(now) {
		t.Fatalf("unexpected verified release: %+v", verified)
	}

	if err := os.WriteFile(artifactPath, []byte("tampered installer fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	_, err = verifier.VerifyFiles(context.Background(), updateverify.VerifyFilesInput{
		ManifestPath: manifestPath, SignaturePath: signaturePath, ArtifactPath: artifactPath,
		ExpectedChannel: releaseupdate.ChannelBeta, ExpectedOS: "windows", ExpectedArchitecture: "amd64", CurrentVersion: "0.32.0",
	})
	if !errors.Is(err, updateverify.ErrArtifactMismatch) {
		t.Fatalf("tampered artifact error = %v", err)
	}
}

func TestVerifierRejectsWrongChannelAndExpiredManifest(t *testing.T) {
	t.Parallel()
	now := time.Date(2026, 7, 19, 12, 0, 0, 0, time.UTC)
	for _, test := range []struct {
		name      string
		channel   releaseupdate.Channel
		expiresAt time.Time
		expected  error
	}{
		{name: "wrong channel", channel: releaseupdate.ChannelAlpha, expiresAt: now.Add(time.Hour), expected: updateverify.ErrChannelMismatch},
		{name: "expired", channel: releaseupdate.ChannelBeta, expiresAt: now.Add(-time.Minute), expected: updateverify.ErrManifestExpired},
	} {
		t.Run(test.name, func(t *testing.T) {
			verifier, input := signedFixture(t, now, test.expiresAt)
			input.ExpectedChannel = test.channel
			_, err := verifier.VerifyFiles(context.Background(), input)
			if !errors.Is(err, test.expected) {
				t.Fatalf("VerifyFiles() error = %v, want %v", err, test.expected)
			}
		})
	}
}

func TestDecodeManifestRejectsUnknownFields(t *testing.T) {
	t.Parallel()
	encoded := []byte(`{"schema":"talos.update-manifest/1","release_id":"019f5f1a-1000-7000-8000-000000000001","channel":"beta","version":"0.33.0","minimum_current_version":"0.32.0","published_at":"2026-07-19T11:00:00Z","expires_at":"2026-07-19T13:00:00Z","target":{"os":"windows","architecture":"amd64"},"artifact":{"name":"talos-setup.exe","size_bytes":1,"sha256":"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"},"unexpected":true}`)
	if _, err := decodeManifest(encoded); !errors.Is(err, updateverify.ErrManifestInvalid) {
		t.Fatalf("decodeManifest() error = %v", err)
	}
}

func TestManifestContractFixturesStayAligned(t *testing.T) {
	t.Parallel()
	root := filepath.Join("..", "..", "..", "contracts")
	schemaBytes, err := os.ReadFile(filepath.Join(root, "jsonschema", "update", "v1", "update-manifest.schema.json"))
	if err != nil {
		t.Fatal(err)
	}
	var schema map[string]any
	if json.Unmarshal(schemaBytes, &schema) != nil || schema["$schema"] != "https://json-schema.org/draft/2020-12/schema" || schema["additionalProperties"] != false {
		t.Fatal("update manifest schema contract is invalid")
	}
	valid, err := os.ReadFile(filepath.Join(root, "fixtures", "update", "v1", "valid-update-manifest.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeManifest(valid); err != nil {
		t.Fatalf("valid update manifest fixture: %v", err)
	}
	invalid, err := os.ReadFile(filepath.Join(root, "fixtures", "update", "v1", "invalid-update-manifest-unknown-field.json"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := decodeManifest(invalid); !errors.Is(err, updateverify.ErrManifestInvalid) {
		t.Fatalf("invalid update manifest fixture error = %v", err)
	}
}

func signedFixture(t *testing.T, now, expiresAt time.Time) (*Verifier, updateverify.VerifyFilesInput) {
	t.Helper()
	publicKey, privateKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	artifactPath := filepath.Join(root, "talos-setup.exe")
	artifact := []byte("signed installer fixture")
	if err := os.WriteFile(artifactPath, artifact, 0o600); err != nil {
		t.Fatal(err)
	}
	artifactHash := sha256.Sum256(artifact)
	releaseID, err := id.UUIDv7(now, rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	manifest := releaseupdate.Manifest{
		Schema: releaseupdate.ManifestSchema, ReleaseID: releaseID, Channel: releaseupdate.ChannelBeta,
		Version: "0.33.0", MinimumCurrentVersion: "0.32.0", PublishedAt: now.Add(-2 * time.Hour), ExpiresAt: expiresAt,
		Target:   releaseupdate.Target{OS: "windows", Architecture: "amd64"},
		Artifact: releaseupdate.Artifact{Name: filepath.Base(artifactPath), SizeBytes: int64(len(artifact)), SHA256: hex.EncodeToString(artifactHash[:])},
	}
	manifestBytes, err := json.Marshal(manifest)
	if err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(root, "manifest.json")
	signaturePath := filepath.Join(root, "manifest.sig")
	if err := os.WriteFile(manifestPath, manifestBytes, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(signaturePath, []byte(base64.StdEncoding.EncodeToString(ed25519.Sign(privateKey, manifestBytes))), 0o600); err != nil {
		t.Fatal(err)
	}
	verifier, err := New(publicKey, func() time.Time { return now })
	if err != nil {
		t.Fatal(err)
	}
	return verifier, updateverify.VerifyFilesInput{
		ManifestPath: manifestPath, SignaturePath: signaturePath, ArtifactPath: artifactPath,
		ExpectedChannel: releaseupdate.ChannelBeta, ExpectedOS: "windows", ExpectedArchitecture: "amd64", CurrentVersion: "0.32.0",
	}
}
