package syncpack

import (
	"bytes"
	"context"
	"crypto/ed25519"
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

	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
)

const (
	PackSchema       = "talos.sync-pack/1"
	ManifestSchema   = "talos.sync-manifest/1"
	PayloadSchema    = "talos.sync-payload/1"
	MaxEventsPerPack = 512
	MaxPackBytes     = 16 << 20
)

var (
	ErrInvalidPack      = errors.New("invalid sync pack")
	ErrSignatureInvalid = errors.New("sync pack signature is invalid")
	ErrHashMismatch     = errors.New("sync pack ciphertext hash mismatch")
	ErrSequenceInvalid  = errors.New("sync pack sequence is invalid")
)

type Event struct {
	DeviceSeq     uint64            `json:"device_seq"`
	EventID       string            `json:"event_id"`
	Type          string            `json:"type"`
	SchemaVersion int               `json:"schema_version"`
	Sensitivity   event.Sensitivity `json:"sensitivity"`
	Payload       []byte            `json:"payload"`
	OccurredAt    string            `json:"occurred_at"`
}

type Manifest struct {
	Schema         string `json:"schema"`
	PackID         string `json:"pack_id"`
	VaultID        string `json:"vault_id"`
	DeviceID       string `json:"device_id"`
	SequenceStart  uint64 `json:"sequence_start"`
	SequenceEnd    uint64 `json:"sequence_end"`
	EventCount     int    `json:"event_count"`
	CiphertextHash string `json:"ciphertext_hash"`
	CreatedAt      string `json:"created_at"`
	Signature      string `json:"signature"`
}

type Pack struct {
	Schema     string   `json:"schema"`
	Manifest   Manifest `json:"manifest"`
	Ciphertext []byte   `json:"ciphertext"`
}

type ExportInput struct {
	VaultID       string
	DeviceID      string
	SequenceStart uint64
	SequenceEnd   uint64
	Events        []Event
	CreatedAt     time.Time
	EncryptionKey []byte
	SigningKey    ed25519.PrivateKey
}

type ImportInput struct {
	Encoded       []byte
	VaultID       string
	DeviceID      string
	EncryptionKey []byte
	VerifyKey     ed25519.PublicKey
}

type Codec struct{ random io.Reader }

func New() *Codec { return &Codec{random: rand.Reader} }

func NewWithRandom(random io.Reader) (*Codec, error) {
	if random == nil {
		return nil, ErrInvalidPack
	}
	return &Codec{random: random}, nil
}

func (c *Codec) Export(ctx context.Context, input ExportInput) ([]byte, Manifest, error) {
	if ctx == nil || c == nil || c.random == nil || len(input.EncryptionKey) != 32 || len(input.SigningKey) != ed25519.PrivateKeySize || strings.TrimSpace(input.VaultID) == "" || strings.TrimSpace(input.DeviceID) == "" || input.CreatedAt.IsZero() || len(input.Events) == 0 || len(input.Events) > MaxEventsPerPack {
		return nil, Manifest{}, ErrInvalidPack
	}
	if err := ctx.Err(); err != nil {
		return nil, Manifest{}, err
	}
	if err := validateSequence(input.Events, input.SequenceStart, input.SequenceEnd); err != nil {
		return nil, Manifest{}, err
	}
	payload, err := json.Marshal(struct {
		Schema string  `json:"schema"`
		Events []Event `json:"events"`
	}{PayloadSchema, input.Events})
	if err != nil || len(payload) > MaxPackBytes {
		return nil, Manifest{}, ErrInvalidPack
	}
	defer clear(payload)
	objectID := packObjectID(input.DeviceID, input.SequenceStart, input.SequenceEnd)
	sealer, err := envelope.NewSealerWithRandom("sync-pack-v1", input.EncryptionKey, c.random)
	if err != nil {
		return nil, Manifest{}, err
	}
	defer sealer.Destroy()
	ciphertext, err := sealer.Seal(payload, envelope.AAD{VaultID: input.VaultID, ObjectID: objectID, SchemaVersion: 1, Sensitivity: "sensitive"})
	if err != nil {
		return nil, Manifest{}, err
	}
	hash := sha256.Sum256(ciphertext)
	manifest := Manifest{Schema: ManifestSchema, VaultID: input.VaultID, DeviceID: input.DeviceID, SequenceStart: input.SequenceStart, SequenceEnd: input.SequenceEnd, EventCount: len(input.Events), CiphertextHash: hex.EncodeToString(hash[:]), CreatedAt: input.CreatedAt.UTC().Format(time.RFC3339Nano)}
	manifest.PackID = derivePackID(manifest)
	signed, err := manifestSigningBytes(manifest)
	if err != nil {
		return nil, Manifest{}, err
	}
	manifest.Signature = base64.RawStdEncoding.EncodeToString(ed25519.Sign(input.SigningKey, signed))
	encoded, err := json.Marshal(Pack{Schema: PackSchema, Manifest: manifest, Ciphertext: ciphertext})
	if err != nil || len(encoded) > MaxPackBytes {
		return nil, Manifest{}, ErrInvalidPack
	}
	return encoded, manifest, nil
}

func (c *Codec) Import(ctx context.Context, input ImportInput) (Manifest, []Event, error) {
	if ctx == nil || len(input.Encoded) == 0 || len(input.Encoded) > MaxPackBytes || len(input.EncryptionKey) != 32 || len(input.VerifyKey) != ed25519.PublicKeySize || input.VaultID == "" || input.DeviceID == "" {
		return Manifest{}, nil, ErrInvalidPack
	}
	if err := ctx.Err(); err != nil {
		return Manifest{}, nil, err
	}
	var pack Pack
	if err := decodeStrict(input.Encoded, &pack); err != nil || pack.Schema != PackSchema || pack.Manifest.Schema != ManifestSchema || pack.Manifest.VaultID != input.VaultID || pack.Manifest.DeviceID != input.DeviceID || pack.Manifest.EventCount < 1 || pack.Manifest.EventCount > MaxEventsPerPack || pack.Manifest.SequenceStart < 1 || pack.Manifest.SequenceEnd < pack.Manifest.SequenceStart || pack.Manifest.PackID != derivePackID(pack.Manifest) {
		return Manifest{}, nil, ErrInvalidPack
	}
	hash := sha256.Sum256(pack.Ciphertext)
	if pack.Manifest.CiphertextHash != hex.EncodeToString(hash[:]) {
		return Manifest{}, nil, ErrHashMismatch
	}
	signature, err := base64.RawStdEncoding.DecodeString(pack.Manifest.Signature)
	if err != nil {
		return Manifest{}, nil, ErrSignatureInvalid
	}
	signed, err := manifestSigningBytes(pack.Manifest)
	if err != nil || !ed25519.Verify(input.VerifyKey, signed, signature) {
		return Manifest{}, nil, ErrSignatureInvalid
	}
	sealer, err := envelope.NewSealer("sync-pack-v1", input.EncryptionKey)
	if err != nil {
		return Manifest{}, nil, err
	}
	defer sealer.Destroy()
	plaintext, err := sealer.Open(pack.Ciphertext, envelope.AAD{VaultID: input.VaultID, ObjectID: packObjectID(input.DeviceID, pack.Manifest.SequenceStart, pack.Manifest.SequenceEnd), SchemaVersion: 1, Sensitivity: "sensitive"})
	if err != nil {
		return Manifest{}, nil, err
	}
	defer clear(plaintext)
	var payload struct {
		Schema string  `json:"schema"`
		Events []Event `json:"events"`
	}
	if err := decodeStrict(plaintext, &payload); err != nil || payload.Schema != PayloadSchema || len(payload.Events) != pack.Manifest.EventCount {
		return Manifest{}, nil, ErrInvalidPack
	}
	if err := validateSequence(payload.Events, pack.Manifest.SequenceStart, pack.Manifest.SequenceEnd); err != nil {
		return Manifest{}, nil, err
	}
	return pack.Manifest, payload.Events, nil
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
			return ErrInvalidPack
		}
		return err
	}
	return nil
}

func validateSequence(events []Event, start, end uint64) error {
	if len(events) == 0 || start < 1 || end < start || uint64(len(events)) != end-start+1 {
		return ErrSequenceInvalid
	}
	for index, candidate := range events {
		if candidate.DeviceSeq != start+uint64(index) {
			return ErrSequenceInvalid
		}
		occurredAt, err := time.Parse(time.RFC3339Nano, candidate.OccurredAt)
		record := event.Record{ID: candidate.EventID, VaultID: "sync-validation", Type: candidate.Type, SchemaVersion: candidate.SchemaVersion, Sensitivity: candidate.Sensitivity, Payload: candidate.Payload, OccurredAt: occurredAt}
		if err != nil || record.Validate() != nil {
			return ErrInvalidPack
		}
	}
	return nil
}

func packObjectID(deviceID string, start, end uint64) string {
	return fmt.Sprintf("sync:%s:%d-%d", deviceID, start, end)
}

func derivePackID(manifest Manifest) string {
	value := fmt.Sprintf("%s\x00%s\x00%d\x00%d\x00%s", manifest.VaultID, manifest.DeviceID, manifest.SequenceStart, manifest.SequenceEnd, manifest.CiphertextHash)
	hash := sha256.Sum256([]byte(value))
	return "sha256:" + hex.EncodeToString(hash[:])
}

func manifestSigningBytes(manifest Manifest) ([]byte, error) {
	manifest.Signature = ""
	return json.Marshal(manifest)
}
