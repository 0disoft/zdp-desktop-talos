package envelope

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"io"
)

const (
	Version   = 1
	Algorithm = "AES-256-GCM+DEK"
	keySize   = 32
)

var (
	ErrInvalidKey      = errors.New("envelope key must contain exactly 32 bytes")
	ErrInvalidEnvelope = errors.New("invalid encrypted envelope")
)

type AAD struct {
	VaultID       string `json:"vault_id"`
	ObjectID      string `json:"object_id"`
	SchemaVersion int    `json:"schema_version"`
	Sensitivity   string `json:"sensitivity"`
}

type Ciphertext struct {
	Version       int    `json:"version"`
	Algorithm     string `json:"algorithm"`
	KeyID         string `json:"key_id"`
	WrapNonce     []byte `json:"wrap_nonce"`
	WrappedDEK    []byte `json:"wrapped_dek"`
	PayloadNonce  []byte `json:"payload_nonce"`
	PayloadCipher []byte `json:"payload_cipher"`
}

type Sealer struct {
	keyID  string
	kek    [keySize]byte
	random io.Reader
}

func (s *Sealer) Destroy() {
	if s == nil {
		return
	}
	clear(s.kek[:])
}

func NewSealer(keyID string, key []byte) (*Sealer, error) {
	return NewSealerWithRandom(keyID, key, rand.Reader)
}

func NewSealerWithRandom(keyID string, key []byte, random io.Reader) (*Sealer, error) {
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	if keyID == "" || random == nil {
		return nil, fmt.Errorf("%w: key id and random source are required", ErrInvalidEnvelope)
	}

	sealer := &Sealer{keyID: keyID, random: random}
	copy(sealer.kek[:], key)
	return sealer, nil
}

func (s *Sealer) Seal(plaintext []byte, metadata AAD) ([]byte, error) {
	aad, err := metadata.bytes()
	if err != nil {
		return nil, err
	}

	dek := make([]byte, keySize)
	if _, err := io.ReadFull(s.random, dek); err != nil {
		return nil, fmt.Errorf("generate data key: %w", err)
	}

	payloadAEAD, err := newAEAD(dek)
	if err != nil {
		return nil, err
	}
	payloadNonce, err := s.nonce(payloadAEAD)
	if err != nil {
		return nil, err
	}
	payloadCipher := payloadAEAD.Seal(nil, payloadNonce, plaintext, aad)

	wrapAEAD, err := newAEAD(s.kek[:])
	if err != nil {
		return nil, err
	}
	wrapNonce, err := s.nonce(wrapAEAD)
	if err != nil {
		return nil, err
	}
	wrappedDEK := wrapAEAD.Seal(nil, wrapNonce, dek, wrapAAD(aad))

	encoded, err := json.Marshal(Ciphertext{
		Version:       Version,
		Algorithm:     Algorithm,
		KeyID:         s.keyID,
		WrapNonce:     wrapNonce,
		WrappedDEK:    wrappedDEK,
		PayloadNonce:  payloadNonce,
		PayloadCipher: payloadCipher,
	})
	if err != nil {
		return nil, fmt.Errorf("encode envelope: %w", err)
	}
	return encoded, nil
}

func (s *Sealer) Open(encoded []byte, metadata AAD) ([]byte, error) {
	aad, err := metadata.bytes()
	if err != nil {
		return nil, err
	}

	var ciphertext Ciphertext
	if err := json.Unmarshal(encoded, &ciphertext); err != nil {
		return nil, fmt.Errorf("%w: decode: %v", ErrInvalidEnvelope, err)
	}
	if ciphertext.Version != Version || ciphertext.Algorithm != Algorithm || ciphertext.KeyID != s.keyID {
		return nil, fmt.Errorf("%w: unsupported version, algorithm, or key id", ErrInvalidEnvelope)
	}

	wrapAEAD, err := newAEAD(s.kek[:])
	if err != nil {
		return nil, err
	}
	if len(ciphertext.WrapNonce) != wrapAEAD.NonceSize() {
		return nil, fmt.Errorf("%w: invalid wrap nonce", ErrInvalidEnvelope)
	}
	dek, err := wrapAEAD.Open(nil, ciphertext.WrapNonce, ciphertext.WrappedDEK, wrapAAD(aad))
	if err != nil {
		return nil, fmt.Errorf("%w: unwrap data key", ErrInvalidEnvelope)
	}

	payloadAEAD, err := newAEAD(dek)
	if err != nil {
		return nil, fmt.Errorf("%w: invalid data key", ErrInvalidEnvelope)
	}
	if len(ciphertext.PayloadNonce) != payloadAEAD.NonceSize() {
		return nil, fmt.Errorf("%w: invalid payload nonce", ErrInvalidEnvelope)
	}
	plaintext, err := payloadAEAD.Open(nil, ciphertext.PayloadNonce, ciphertext.PayloadCipher, aad)
	if err != nil {
		return nil, fmt.Errorf("%w: decrypt payload", ErrInvalidEnvelope)
	}
	return plaintext, nil
}

func (s *Sealer) nonce(aead cipher.AEAD) ([]byte, error) {
	nonce := make([]byte, aead.NonceSize())
	if _, err := io.ReadFull(s.random, nonce); err != nil {
		return nil, fmt.Errorf("generate nonce: %w", err)
	}
	return nonce, nil
}

func (a AAD) bytes() ([]byte, error) {
	if a.VaultID == "" || a.ObjectID == "" || a.SchemaVersion < 1 || a.Sensitivity == "" {
		return nil, fmt.Errorf("%w: incomplete associated data", ErrInvalidEnvelope)
	}
	encoded, err := json.Marshal(a)
	if err != nil {
		return nil, fmt.Errorf("encode associated data: %w", err)
	}
	return encoded, nil
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func wrapAAD(aad []byte) []byte {
	wrapped := make([]byte, 0, len(aad)+5)
	wrapped = append(wrapped, aad...)
	return append(wrapped, 0, 'd', 'e', 'k', 0)
}
