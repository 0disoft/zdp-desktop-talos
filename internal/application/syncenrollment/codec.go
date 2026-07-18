package syncenrollment

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/application/syncidentity"
	domain "github.com/0disoft/zdp-desktop-talos/internal/domain/syncenrollment"
	"github.com/0disoft/zdp-desktop-talos/internal/id"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

const (
	PackageSchema    = "talos.sync-enrollment-package/1"
	OfferSchema      = "talos.sync-enrollment-offer/1"
	AcceptanceSchema = "talos.sync-enrollment-acceptance/1"
	secretPrefix     = "talos-enroll-v1."
	saltBytes        = 32
)

var (
	ErrInvalidPackage   = errors.New("invalid sync enrollment package")
	ErrInvalidSecret    = errors.New("invalid sync enrollment secret")
	ErrSignatureInvalid = errors.New("sync enrollment signature is invalid")
	ErrExpired          = errors.New("sync enrollment has expired or is not active yet")
)

type Kind string

const (
	KindOffer      Kind = "offer"
	KindAcceptance Kind = "acceptance"
)

type Package struct {
	Schema       string `json:"schema"`
	Kind         Kind   `json:"kind"`
	EnrollmentID string `json:"enrollment_id"`
	Salt         []byte `json:"salt"`
	Ciphertext   []byte `json:"ciphertext"`
}

type OfferResult struct {
	Offer   domain.Offer
	Encoded []byte
	Secret  string
	Hash    string
}

type AcceptanceResult struct {
	Acceptance domain.Acceptance
	Encoded    []byte
	Hash       string
}

type CreateOfferInput struct {
	VaultID         string
	VaultCreatedAt  time.Time
	RetentionDays   int
	VaultKey        []byte
	SourceDeviceID  string
	SourcePublicKey ed25519.PublicKey
	SigningKey      ed25519.PrivateKey
	IssuedAt        time.Time
	ValidFor        time.Duration
}

type CreateAcceptanceInput struct {
	Offer           domain.Offer
	OfferHash       string
	TargetDeviceID  string
	TargetPublicKey ed25519.PublicKey
	SigningKey      ed25519.PrivateKey
	AcceptedAt      time.Time
	Secret          string
}

type Codec struct{ random io.Reader }

func NewCodec() *Codec { return &Codec{random: rand.Reader} }

func NewCodecWithRandom(random io.Reader) (*Codec, error) {
	if random == nil {
		return nil, ErrInvalidPackage
	}
	return &Codec{random: random}, nil
}

func (c *Codec) CreateOffer(ctx context.Context, input CreateOfferInput) (OfferResult, error) {
	if c == nil || c.random == nil || ctx == nil || input.ValidFor < domain.MinValidity || input.ValidFor > domain.MaxValidity || len(input.VaultKey) != 32 || len(input.SourcePublicKey) != ed25519.PublicKeySize || len(input.SigningKey) != ed25519.PrivateKeySize || syncidentity.DeviceID(input.SourcePublicKey) != input.SourceDeviceID {
		return OfferResult{}, ErrInvalidPackage
	}
	if err := ctx.Err(); err != nil {
		return OfferResult{}, err
	}
	enrollmentID, err := id.UUIDv7(input.IssuedAt.UTC(), c.random)
	if err != nil {
		return OfferResult{}, fmt.Errorf("generate enrollment ID: %w", err)
	}
	offer := domain.Offer{EnrollmentID: enrollmentID, VaultID: input.VaultID, VaultCreatedAt: input.VaultCreatedAt.UTC(), RetentionDays: input.RetentionDays, SourceDeviceID: input.SourceDeviceID, SourcePublicKey: append(ed25519.PublicKey(nil), input.SourcePublicKey...), VaultKey: append([]byte(nil), input.VaultKey...), IssuedAt: input.IssuedAt.UTC(), ExpiresAt: input.IssuedAt.UTC().Add(input.ValidFor)}
	defer clear(offer.VaultKey)
	if offer.Validate() != nil {
		return OfferResult{}, ErrInvalidPackage
	}
	payload := offerPayloadFromDomain(offer)
	signed, err := offerSigningBytes(payload)
	if err != nil {
		return OfferResult{}, ErrInvalidPackage
	}
	payload.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(input.SigningKey, signed))
	secret, secretText, err := c.newSecret()
	if err != nil {
		return OfferResult{}, err
	}
	defer clear(secret)
	encoded, err := c.seal(ctx, KindOffer, enrollmentID, payload, secret)
	if err != nil {
		return OfferResult{}, err
	}
	return OfferResult{Offer: cloneOffer(offer), Encoded: encoded, Secret: secretText, Hash: hashBytes(encoded)}, nil
}

func (c *Codec) OpenOffer(ctx context.Context, encoded []byte, secretText string) (domain.Offer, string, error) {
	var payload offerPayload
	outerEnrollmentID, err := c.open(ctx, KindOffer, encoded, secretText, &payload)
	if err != nil {
		return domain.Offer{}, "", err
	}
	if payload.Schema != OfferSchema || payload.EnrollmentID != outerEnrollmentID {
		return domain.Offer{}, "", ErrInvalidPackage
	}
	signed, err := offerSigningBytes(payload)
	if err != nil {
		return domain.Offer{}, "", ErrInvalidPackage
	}
	publicKey, err := base64.RawStdEncoding.DecodeString(payload.SourcePublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return domain.Offer{}, "", ErrInvalidPackage
	}
	signature, err := base64.RawStdEncoding.DecodeString(payload.Signature)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), signed, signature) {
		return domain.Offer{}, "", ErrSignatureInvalid
	}
	offer, err := payload.domain()
	if err != nil || syncidentity.DeviceID(offer.SourcePublicKey) != offer.SourceDeviceID {
		clear(offer.VaultKey)
		return domain.Offer{}, "", ErrInvalidPackage
	}
	return offer, hashBytes(encoded), nil
}

func (c *Codec) CreateAcceptance(ctx context.Context, input CreateAcceptanceInput) (AcceptanceResult, error) {
	if c == nil || c.random == nil || ctx == nil || input.Offer.Validate() != nil || !validHash(input.OfferHash) || len(input.TargetPublicKey) != ed25519.PublicKeySize || len(input.SigningKey) != ed25519.PrivateKeySize || syncidentity.DeviceID(input.TargetPublicKey) != input.TargetDeviceID || input.AcceptedAt.UTC().Before(input.Offer.IssuedAt.Add(-domain.MaxClockSkew)) || input.AcceptedAt.UTC().After(input.Offer.ExpiresAt) {
		return AcceptanceResult{}, ErrInvalidPackage
	}
	acceptance := domain.Acceptance{EnrollmentID: input.Offer.EnrollmentID, VaultID: input.Offer.VaultID, OfferHash: input.OfferHash, SourceDeviceID: input.Offer.SourceDeviceID, TargetDeviceID: input.TargetDeviceID, TargetPublicKey: append(ed25519.PublicKey(nil), input.TargetPublicKey...), AcceptedAt: input.AcceptedAt.UTC(), ExpiresAt: input.Offer.ExpiresAt.UTC()}
	if acceptance.Validate() != nil {
		return AcceptanceResult{}, ErrInvalidPackage
	}
	payload := acceptancePayloadFromDomain(acceptance)
	signed, err := acceptanceSigningBytes(payload)
	if err != nil {
		return AcceptanceResult{}, ErrInvalidPackage
	}
	payload.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(input.SigningKey, signed))
	secret, err := parseSecret(input.Secret)
	if err != nil {
		return AcceptanceResult{}, err
	}
	defer clear(secret)
	encoded, err := c.seal(ctx, KindAcceptance, acceptance.EnrollmentID, payload, secret)
	if err != nil {
		return AcceptanceResult{}, err
	}
	return AcceptanceResult{Acceptance: acceptance, Encoded: encoded, Hash: hashBytes(encoded)}, nil
}

func (c *Codec) OpenAcceptance(ctx context.Context, encoded []byte, secretText string) (domain.Acceptance, string, error) {
	var payload acceptancePayload
	outerEnrollmentID, err := c.open(ctx, KindAcceptance, encoded, secretText, &payload)
	if err != nil {
		return domain.Acceptance{}, "", err
	}
	if payload.Schema != AcceptanceSchema || payload.EnrollmentID != outerEnrollmentID {
		return domain.Acceptance{}, "", ErrInvalidPackage
	}
	signed, err := acceptanceSigningBytes(payload)
	if err != nil {
		return domain.Acceptance{}, "", ErrInvalidPackage
	}
	publicKey, err := base64.RawStdEncoding.DecodeString(payload.TargetPublicKey)
	if err != nil || len(publicKey) != ed25519.PublicKeySize {
		return domain.Acceptance{}, "", ErrInvalidPackage
	}
	signature, err := base64.RawStdEncoding.DecodeString(payload.Signature)
	if err != nil || !ed25519.Verify(ed25519.PublicKey(publicKey), signed, signature) {
		return domain.Acceptance{}, "", ErrSignatureInvalid
	}
	acceptance, err := payload.domain()
	if err != nil || syncidentity.DeviceID(acceptance.TargetPublicKey) != acceptance.TargetDeviceID {
		return domain.Acceptance{}, "", ErrInvalidPackage
	}
	return acceptance, hashBytes(encoded), nil
}

func RequireActiveOffer(offer domain.Offer, now time.Time) error {
	if !offer.ActiveAt(now.UTC()) {
		return ErrExpired
	}
	return nil
}

func RequireActiveAcceptance(acceptance domain.Acceptance, now time.Time) error {
	if !acceptance.ActiveAt(now.UTC()) {
		return ErrExpired
	}
	return nil
}

func (c *Codec) seal(ctx context.Context, kind Kind, enrollmentID string, payload any, secret []byte) ([]byte, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(payload)
	if err != nil || len(plaintext) == 0 || len(plaintext) > domain.MaxPackageBytes/2 {
		return nil, ErrInvalidPackage
	}
	defer clear(plaintext)
	salt := make([]byte, saltBytes)
	if _, err := io.ReadFull(c.random, salt); err != nil {
		return nil, fmt.Errorf("generate enrollment salt: %w", err)
	}
	key := deriveKey(secret, salt, kind, enrollmentID)
	defer clear(key)
	sealer, err := envelope.NewSealerWithRandom("sync-enrollment-v1", key, c.random)
	if err != nil {
		return nil, err
	}
	defer sealer.Destroy()
	ciphertext, err := sealer.Seal(plaintext, enrollmentAAD(kind, enrollmentID))
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(Package{Schema: PackageSchema, Kind: kind, EnrollmentID: enrollmentID, Salt: salt, Ciphertext: ciphertext})
	if err != nil || len(encoded) > domain.MaxPackageBytes {
		return nil, ErrInvalidPackage
	}
	return encoded, nil
}

func (c *Codec) open(ctx context.Context, expectedKind Kind, encoded []byte, secretText string, destination any) (string, error) {
	if c == nil || ctx == nil || len(encoded) == 0 || len(encoded) > domain.MaxPackageBytes {
		return "", ErrInvalidPackage
	}
	if err := ctx.Err(); err != nil {
		return "", err
	}
	var pack Package
	if err := decodeStrict(encoded, &pack); err != nil || pack.Schema != PackageSchema || pack.Kind != expectedKind || !id.IsUUIDv7(pack.EnrollmentID) || len(pack.Salt) != saltBytes || len(pack.Ciphertext) == 0 || len(pack.Ciphertext) > domain.MaxPackageBytes {
		return "", ErrInvalidPackage
	}
	secret, err := parseSecret(secretText)
	if err != nil {
		return "", err
	}
	defer clear(secret)
	key := deriveKey(secret, pack.Salt, pack.Kind, pack.EnrollmentID)
	defer clear(key)
	sealer, err := envelope.NewSealer("sync-enrollment-v1", key)
	if err != nil {
		return "", err
	}
	defer sealer.Destroy()
	plaintext, err := sealer.Open(pack.Ciphertext, enrollmentAAD(pack.Kind, pack.EnrollmentID))
	if err != nil {
		return "", ErrInvalidSecret
	}
	defer clear(plaintext)
	if err := decodeStrict(plaintext, destination); err != nil {
		return "", ErrInvalidPackage
	}
	return pack.EnrollmentID, nil
}

func (c *Codec) newSecret() ([]byte, string, error) {
	secret := make([]byte, domain.SecretBytes)
	if _, err := io.ReadFull(c.random, secret); err != nil {
		return nil, "", fmt.Errorf("generate enrollment secret: %w", err)
	}
	return secret, secretPrefix + base64.RawURLEncoding.EncodeToString(secret), nil
}

func parseSecret(value string) ([]byte, error) {
	if !strings.HasPrefix(value, secretPrefix) {
		return nil, ErrInvalidSecret
	}
	decoded, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(value, secretPrefix))
	if err != nil || len(decoded) != domain.SecretBytes || secretPrefix+base64.RawURLEncoding.EncodeToString(decoded) != value {
		clear(decoded)
		return nil, ErrInvalidSecret
	}
	return decoded, nil
}

func deriveKey(secret, salt []byte, kind Kind, enrollmentID string) []byte {
	extract := hmac.New(sha256.New, salt)
	_, _ = extract.Write(secret)
	prk := extract.Sum(nil)
	defer clear(prk)
	expand := hmac.New(sha256.New, prk)
	_, _ = expand.Write([]byte("talos.sync-enrollment/v1\x00" + string(kind) + "\x00" + enrollmentID))
	_, _ = expand.Write([]byte{1})
	return expand.Sum(nil)
}

func enrollmentAAD(kind Kind, enrollmentID string) envelope.AAD {
	return envelope.AAD{VaultID: "sync-enrollment-transfer", ObjectID: string(kind) + ":" + enrollmentID, SchemaVersion: 1, Sensitivity: "secret"}
}

func hashBytes(value []byte) string {
	hash := sha256.Sum256(value)
	return hex.EncodeToString(hash[:])
}

func validHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

type offerPayload struct {
	Schema          string `json:"schema"`
	EnrollmentID    string `json:"enrollment_id"`
	VaultID         string `json:"vault_id"`
	VaultCreatedAt  string `json:"vault_created_at"`
	RetentionDays   int    `json:"retention_days"`
	SourceDeviceID  string `json:"source_device_id"`
	SourcePublicKey string `json:"source_public_key"`
	VaultKey        string `json:"vault_key"`
	IssuedAt        string `json:"issued_at"`
	ExpiresAt       string `json:"expires_at"`
	Signature       string `json:"signature"`
}

func offerPayloadFromDomain(offer domain.Offer) offerPayload {
	return offerPayload{Schema: OfferSchema, EnrollmentID: offer.EnrollmentID, VaultID: offer.VaultID, VaultCreatedAt: offer.VaultCreatedAt.UTC().Format(time.RFC3339Nano), RetentionDays: offer.RetentionDays, SourceDeviceID: offer.SourceDeviceID, SourcePublicKey: base64.RawStdEncoding.EncodeToString(offer.SourcePublicKey), VaultKey: base64.RawStdEncoding.EncodeToString(offer.VaultKey), IssuedAt: offer.IssuedAt.UTC().Format(time.RFC3339Nano), ExpiresAt: offer.ExpiresAt.UTC().Format(time.RFC3339Nano)}
}

func (p offerPayload) domain() (domain.Offer, error) {
	publicKey, err := base64.RawStdEncoding.DecodeString(p.SourcePublicKey)
	if err != nil {
		return domain.Offer{}, err
	}
	vaultKey, err := base64.RawStdEncoding.DecodeString(p.VaultKey)
	if err != nil {
		return domain.Offer{}, err
	}
	issuedAt, err := time.Parse(time.RFC3339Nano, p.IssuedAt)
	if err != nil {
		clear(vaultKey)
		return domain.Offer{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	if err != nil {
		clear(vaultKey)
		return domain.Offer{}, err
	}
	vaultCreatedAt, err := time.Parse(time.RFC3339Nano, p.VaultCreatedAt)
	if err != nil {
		clear(vaultKey)
		return domain.Offer{}, err
	}
	offer := domain.Offer{EnrollmentID: p.EnrollmentID, VaultID: p.VaultID, VaultCreatedAt: vaultCreatedAt, RetentionDays: p.RetentionDays, SourceDeviceID: p.SourceDeviceID, SourcePublicKey: ed25519.PublicKey(publicKey), VaultKey: vaultKey, IssuedAt: issuedAt, ExpiresAt: expiresAt}
	if p.Schema != OfferSchema || offer.Validate() != nil || !id.IsUUIDv7(offer.EnrollmentID) {
		clear(vaultKey)
		return domain.Offer{}, ErrInvalidPackage
	}
	return offer, nil
}

func offerSigningBytes(payload offerPayload) ([]byte, error) {
	payload.Signature = ""
	return json.Marshal(payload)
}

type acceptancePayload struct {
	Schema          string `json:"schema"`
	EnrollmentID    string `json:"enrollment_id"`
	VaultID         string `json:"vault_id"`
	OfferHash       string `json:"offer_hash"`
	SourceDeviceID  string `json:"source_device_id"`
	TargetDeviceID  string `json:"target_device_id"`
	TargetPublicKey string `json:"target_public_key"`
	AcceptedAt      string `json:"accepted_at"`
	ExpiresAt       string `json:"expires_at"`
	Signature       string `json:"signature"`
}

func acceptancePayloadFromDomain(acceptance domain.Acceptance) acceptancePayload {
	return acceptancePayload{Schema: AcceptanceSchema, EnrollmentID: acceptance.EnrollmentID, VaultID: acceptance.VaultID, OfferHash: acceptance.OfferHash, SourceDeviceID: acceptance.SourceDeviceID, TargetDeviceID: acceptance.TargetDeviceID, TargetPublicKey: base64.RawStdEncoding.EncodeToString(acceptance.TargetPublicKey), AcceptedAt: acceptance.AcceptedAt.UTC().Format(time.RFC3339Nano), ExpiresAt: acceptance.ExpiresAt.UTC().Format(time.RFC3339Nano)}
}

func (p acceptancePayload) domain() (domain.Acceptance, error) {
	publicKey, err := base64.RawStdEncoding.DecodeString(p.TargetPublicKey)
	if err != nil {
		return domain.Acceptance{}, err
	}
	acceptedAt, err := time.Parse(time.RFC3339Nano, p.AcceptedAt)
	if err != nil {
		return domain.Acceptance{}, err
	}
	expiresAt, err := time.Parse(time.RFC3339Nano, p.ExpiresAt)
	if err != nil {
		return domain.Acceptance{}, err
	}
	acceptance := domain.Acceptance{EnrollmentID: p.EnrollmentID, VaultID: p.VaultID, OfferHash: p.OfferHash, SourceDeviceID: p.SourceDeviceID, TargetDeviceID: p.TargetDeviceID, TargetPublicKey: ed25519.PublicKey(publicKey), AcceptedAt: acceptedAt, ExpiresAt: expiresAt}
	if p.Schema != AcceptanceSchema || acceptance.Validate() != nil || !id.IsUUIDv7(acceptance.EnrollmentID) {
		return domain.Acceptance{}, ErrInvalidPackage
	}
	return acceptance, nil
}

func acceptanceSigningBytes(payload acceptancePayload) ([]byte, error) {
	payload.Signature = ""
	return json.Marshal(payload)
}

func decodeStrict(data []byte, destination any) error {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(destination); err != nil {
		return err
	}
	var trailing any
	if err := decoder.Decode(&trailing); !errors.Is(err, io.EOF) {
		if err == nil {
			return ErrInvalidPackage
		}
		return err
	}
	return nil
}

func cloneOffer(offer domain.Offer) domain.Offer {
	offer.SourcePublicKey = append(ed25519.PublicKey(nil), offer.SourcePublicKey...)
	offer.VaultKey = append([]byte(nil), offer.VaultKey...)
	return offer
}
