package syncenrollment

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"testing"
	"time"

	domain "github.com/0disoft/zdp-desktop-talos/internal/domain/syncenrollment"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

func TestOfferAcceptanceRoundTripAndTrustBoundaries(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 12, 0, 0, 0, time.UTC)
	sourcePrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x11}, ed25519.SeedSize))
	sourcePublic := sourcePrivate.Public().(ed25519.PublicKey)
	targetPrivate := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x22}, ed25519.SeedSize))
	targetPublic := targetPrivate.Public().(ed25519.PublicKey)
	codec, err := NewCodecWithRandom(bytes.NewReader(bytes.Repeat([]byte{0x43}, 512)))
	if err != nil {
		t.Fatal(err)
	}
	vaultKey := bytes.Repeat([]byte{0x55}, 32)
	created, err := codec.CreateOffer(ctx, CreateOfferInput{VaultID: "vault-sensitive-marker", VaultCreatedAt: now.Add(-time.Hour), RetentionDays: 30, VaultKey: vaultKey, SourceDeviceID: deviceID(sourcePublic), SourcePublicKey: sourcePublic, SigningKey: sourcePrivate, IssuedAt: now, ValidFor: 30 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(created.Encoded, []byte("vault-sensitive-marker")) || bytes.Contains(created.Encoded, []byte(base64.RawStdEncoding.EncodeToString(vaultKey))) || bytes.Contains(created.Encoded, []byte(deviceID(sourcePublic))) || bytes.Contains(created.Encoded, []byte(created.Secret)) {
		t.Fatal("enrollment package leaked protected plaintext")
	}
	offer, offerHash, err := codec.OpenOffer(ctx, created.Encoded, created.Secret)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(offer.VaultKey)
	if offerHash != created.Hash || offer.VaultID != created.Offer.VaultID || !bytes.Equal(offer.VaultKey, vaultKey) || RequireActiveOffer(offer, now.Add(time.Minute)) != nil {
		t.Fatalf("offer=%+v hash=%s", offer, offerHash)
	}
	accepted, err := codec.CreateAcceptance(ctx, CreateAcceptanceInput{Offer: offer, OfferHash: offerHash, TargetDeviceID: deviceID(targetPublic), TargetPublicKey: targetPublic, SigningKey: targetPrivate, AcceptedAt: now.Add(time.Minute), Secret: created.Secret})
	if err != nil {
		t.Fatal(err)
	}
	acceptance, acceptanceHash, err := codec.OpenAcceptance(ctx, accepted.Encoded, created.Secret)
	if err != nil {
		t.Fatal(err)
	}
	if acceptanceHash != accepted.Hash || acceptance.OfferHash != offerHash || acceptance.TargetDeviceID != deviceID(targetPublic) || RequireActiveAcceptance(acceptance, now.Add(2*time.Minute)) != nil {
		t.Fatalf("acceptance=%+v hash=%s", acceptance, acceptanceHash)
	}
	if _, _, err := codec.OpenOffer(ctx, created.Encoded, "talos-enroll-v1."+base64.RawURLEncoding.EncodeToString(bytes.Repeat([]byte{0x99}, domain.SecretBytes))); !errors.Is(err, ErrInvalidSecret) {
		t.Fatalf("wrong secret error=%v", err)
	}
	if err := RequireActiveOffer(offer, offer.ExpiresAt.Add(time.Nanosecond)); !errors.Is(err, ErrExpired) {
		t.Fatalf("expired offer error=%v", err)
	}
}

func TestOfferRejectsUnknownFieldsTamperingAndForgedSignature(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	now := time.Date(2026, 7, 18, 13, 0, 0, 0, time.UTC)
	privateKey := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x31}, ed25519.SeedSize))
	publicKey := privateKey.Public().(ed25519.PublicKey)
	codec, _ := NewCodecWithRandom(bytes.NewReader(bytes.Repeat([]byte{0x62}, 1024)))
	created, err := codec.CreateOffer(ctx, CreateOfferInput{VaultID: "vault-1", VaultCreatedAt: now.Add(-time.Minute), RetentionDays: 14, VaultKey: bytes.Repeat([]byte{0x71}, 32), SourceDeviceID: deviceID(publicKey), SourcePublicKey: publicKey, SigningKey: privateKey, IssuedAt: now, ValidFor: time.Hour})
	if err != nil {
		t.Fatal(err)
	}
	var loose map[string]any
	if err := json.Unmarshal(created.Encoded, &loose); err != nil {
		t.Fatal(err)
	}
	loose["unexpected"] = true
	unknown, _ := json.Marshal(loose)
	if _, _, err := codec.OpenOffer(ctx, unknown, created.Secret); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("unknown field error=%v", err)
	}
	tampered := append([]byte(nil), created.Encoded...)
	tampered[len(tampered)-2] ^= 1
	if _, _, err := codec.OpenOffer(ctx, tampered, created.Secret); err == nil {
		t.Fatal("tampered package was accepted")
	}
	forged := forgeOfferSignature(t, codec, created.Encoded, created.Secret)
	if _, _, err := codec.OpenOffer(ctx, forged, created.Secret); !errors.Is(err, ErrSignatureInvalid) {
		t.Fatalf("forged signature error=%v", err)
	}
	rebound := rebindOfferOuterID(t, created.Encoded, created.Secret, "0190a57e-6d90-7b34-8d42-f151641bd28e")
	if _, _, err := codec.OpenOffer(ctx, rebound, created.Secret); !errors.Is(err, ErrInvalidPackage) {
		t.Fatalf("rebound enrollment ID error=%v", err)
	}
}

func forgeOfferSignature(t *testing.T, codec *Codec, encoded []byte, secretText string) []byte {
	t.Helper()
	var pack Package
	if err := decodeStrict(encoded, &pack); err != nil {
		t.Fatal(err)
	}
	secret, err := parseSecret(secretText)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(secret)
	key := deriveKey(secret, pack.Salt, pack.Kind, pack.EnrollmentID)
	defer clear(key)
	sealer, err := envelope.NewSealer("sync-enrollment-v1", key)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := sealer.Open(pack.Ciphertext, enrollmentAAD(pack.Kind, pack.EnrollmentID))
	sealer.Destroy()
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plaintext)
	var payload offerPayload
	if err := decodeStrict(plaintext, &payload); err != nil {
		t.Fatal(err)
	}
	payload.Signature = base64.RawStdEncoding.EncodeToString(bytes.Repeat([]byte{0x00}, ed25519.SignatureSize))
	mutated, _ := json.Marshal(payload)
	resealer, err := envelope.NewSealerWithRandom("sync-enrollment-v1", key, bytes.NewReader(bytes.Repeat([]byte{0x77}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pack.Ciphertext, err = resealer.Seal(mutated, enrollmentAAD(pack.Kind, pack.EnrollmentID))
	resealer.Destroy()
	if err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(pack)
	return result
}

func rebindOfferOuterID(t *testing.T, encoded []byte, secretText, replacementID string) []byte {
	t.Helper()
	var pack Package
	if err := decodeStrict(encoded, &pack); err != nil {
		t.Fatal(err)
	}
	secret, err := parseSecret(secretText)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(secret)
	oldKey := deriveKey(secret, pack.Salt, pack.Kind, pack.EnrollmentID)
	oldSealer, err := envelope.NewSealer("sync-enrollment-v1", oldKey)
	if err != nil {
		t.Fatal(err)
	}
	plaintext, err := oldSealer.Open(pack.Ciphertext, enrollmentAAD(pack.Kind, pack.EnrollmentID))
	oldSealer.Destroy()
	clear(oldKey)
	if err != nil {
		t.Fatal(err)
	}
	defer clear(plaintext)
	pack.EnrollmentID = replacementID
	newKey := deriveKey(secret, pack.Salt, pack.Kind, pack.EnrollmentID)
	defer clear(newKey)
	newSealer, err := envelope.NewSealerWithRandom("sync-enrollment-v1", newKey, bytes.NewReader(bytes.Repeat([]byte{0x78}, 64)))
	if err != nil {
		t.Fatal(err)
	}
	pack.Ciphertext, err = newSealer.Seal(plaintext, enrollmentAAD(pack.Kind, pack.EnrollmentID))
	newSealer.Destroy()
	if err != nil {
		t.Fatal(err)
	}
	result, _ := json.Marshal(pack)
	return result
}

func deviceID(publicKey ed25519.PublicKey) string {
	hash := sha256.Sum256(publicKey)
	return "device-v1-" + hex.EncodeToString(hash[:])
}
