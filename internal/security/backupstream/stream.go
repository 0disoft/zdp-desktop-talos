package backupstream

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
)

const (
	Version   = 1
	Algorithm = "AES-256-GCM-CHUNKED"
	ChunkSize = 1 << 20

	keySize          = 32
	noncePrefixSize  = 4
	frameHeaderSize  = 13
	maxKeyIDBytes    = 128
	headerFixedBytes = len(magicText) + 2 + 1 + 4 + noncePrefixSize + 12 + 2
	magicText        = "TALOSBKP"
)

var (
	magic            = []byte(magicText)
	ErrInvalidKey    = errors.New("backup stream key must contain exactly 32 bytes")
	ErrInvalidStream = errors.New("invalid encrypted backup stream")
	ErrClosed        = errors.New("backup stream is closed")
)

type Writer struct {
	destination io.Writer
	aead        cipher.AEAD
	headerHash  [sha256.Size]byte
	noncePrefix [noncePrefixSize]byte
	buffer      []byte
	counter     uint64
	closed      bool
	dek         [keySize]byte
}

func NewWriter(destination io.Writer, keyID string, key []byte) (*Writer, error) {
	return NewWriterWithRandom(destination, keyID, key, rand.Reader)
}

func NewWriterWithRandom(destination io.Writer, keyID string, key []byte, random io.Reader) (*Writer, error) {
	if destination == nil || random == nil || len(keyID) == 0 || len(keyID) > maxKeyIDBytes {
		return nil, fmt.Errorf("%w: destination, key id, and random source are required", ErrInvalidStream)
	}
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}

	writer := &Writer{destination: destination, buffer: make([]byte, 0, ChunkSize)}
	if _, err := io.ReadFull(random, writer.dek[:]); err != nil {
		writer.destroy()
		return nil, fmt.Errorf("generate backup data key: %w", err)
	}
	if _, err := io.ReadFull(random, writer.noncePrefix[:]); err != nil {
		writer.destroy()
		return nil, fmt.Errorf("generate backup nonce prefix: %w", err)
	}

	wrapAEAD, err := newAEAD(key)
	if err != nil {
		writer.destroy()
		return nil, err
	}
	wrapNonce := make([]byte, wrapAEAD.NonceSize())
	if _, err := io.ReadFull(random, wrapNonce); err != nil {
		writer.destroy()
		return nil, fmt.Errorf("generate backup wrapping nonce: %w", err)
	}
	prefix, err := encodeHeaderPrefix(keyID, writer.noncePrefix, wrapNonce)
	if err != nil {
		writer.destroy()
		return nil, err
	}
	wrappedDEK := wrapAEAD.Seal(nil, wrapNonce, writer.dek[:], prefix)
	header := append(prefix, wrappedDEK...)
	if err := writeFull(destination, header); err != nil {
		writer.destroy()
		return nil, fmt.Errorf("write backup stream header: %w", err)
	}

	payloadAEAD, err := newAEAD(writer.dek[:])
	if err != nil {
		writer.destroy()
		return nil, err
	}
	writer.aead = payloadAEAD
	writer.headerHash = sha256.Sum256(header)
	return writer, nil
}

func (w *Writer) Write(payload []byte) (int, error) {
	if w == nil || w.closed {
		return 0, ErrClosed
	}
	written := 0
	for len(payload) > 0 {
		available := ChunkSize - len(w.buffer)
		count := len(payload)
		if count > available {
			count = available
		}
		w.buffer = append(w.buffer, payload[:count]...)
		payload = payload[count:]
		written += count
		if len(w.buffer) == ChunkSize {
			if err := w.writeFrame(0, w.buffer); err != nil {
				return written, err
			}
			w.buffer = w.buffer[:0]
		}
	}
	return written, nil
}

func (w *Writer) Close() error {
	if w == nil || w.closed {
		return ErrClosed
	}
	w.closed = true
	defer w.destroy()
	if len(w.buffer) > 0 {
		if err := w.writeFrame(0, w.buffer); err != nil {
			return err
		}
	}
	if err := w.writeFrame(1, nil); err != nil {
		return err
	}
	return nil
}

func (w *Writer) writeFrame(flags byte, plaintext []byte) error {
	if flags > 1 || len(plaintext) > ChunkSize || flags == 1 && len(plaintext) != 0 || flags == 0 && len(plaintext) == 0 {
		return ErrInvalidStream
	}
	frameHeader := make([]byte, frameHeaderSize)
	binary.BigEndian.PutUint64(frameHeader[:8], w.counter)
	frameHeader[8] = flags
	binary.BigEndian.PutUint32(frameHeader[9:], uint32(len(plaintext)))
	nonce := frameNonce(w.noncePrefix, w.counter)
	ciphertext := w.aead.Seal(nil, nonce[:], plaintext, frameAAD(w.headerHash, frameHeader))
	if err := writeFull(w.destination, frameHeader); err != nil {
		return fmt.Errorf("write backup frame header: %w", err)
	}
	if err := writeFull(w.destination, ciphertext); err != nil {
		return fmt.Errorf("write backup frame ciphertext: %w", err)
	}
	w.counter++
	return nil
}

func (w *Writer) destroy() {
	if w == nil {
		return
	}
	clear(w.dek[:])
	clear(w.buffer)
	w.buffer = nil
	w.aead = nil
}

type Reader struct {
	source      io.Reader
	aead        cipher.AEAD
	headerHash  [sha256.Size]byte
	noncePrefix [noncePrefixSize]byte
	pending     []byte
	counter     uint64
	finished    bool
	closed      bool
	dek         [keySize]byte
}

func NewReader(source io.Reader, expectedKeyID string, key []byte) (*Reader, error) {
	if source == nil || len(expectedKeyID) == 0 || len(expectedKeyID) > maxKeyIDBytes {
		return nil, fmt.Errorf("%w: source and expected key id are required", ErrInvalidStream)
	}
	if len(key) != keySize {
		return nil, ErrInvalidKey
	}
	header, keyID, noncePrefix, wrapNonce, wrappedDEK, err := readHeader(source)
	if err != nil {
		return nil, err
	}
	if keyID != expectedKeyID {
		return nil, fmt.Errorf("%w: unexpected key id", ErrInvalidStream)
	}
	wrapAEAD, err := newAEAD(key)
	if err != nil {
		return nil, err
	}
	prefix := header[:len(header)-len(wrappedDEK)]
	dek, err := wrapAEAD.Open(nil, wrapNonce, wrappedDEK, prefix)
	if err != nil || len(dek) != keySize {
		clear(dek)
		return nil, fmt.Errorf("%w: unwrap backup data key", ErrInvalidStream)
	}
	reader := &Reader{source: source, headerHash: sha256.Sum256(header), noncePrefix: noncePrefix}
	copy(reader.dek[:], dek)
	clear(dek)
	reader.aead, err = newAEAD(reader.dek[:])
	if err != nil {
		reader.Close()
		return nil, err
	}
	return reader, nil
}

func (r *Reader) Read(destination []byte) (int, error) {
	if r == nil || r.closed {
		return 0, ErrClosed
	}
	if len(destination) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		if r.finished {
			return 0, io.EOF
		}
		if err := r.readFrame(); err != nil {
			return 0, err
		}
	}
	count := copy(destination, r.pending)
	clear(r.pending[:count])
	r.pending = r.pending[count:]
	return count, nil
}

func (r *Reader) Close() error {
	if r == nil || r.closed {
		return nil
	}
	r.closed = true
	clear(r.dek[:])
	clear(r.pending)
	r.pending = nil
	r.aead = nil
	return nil
}

func (r *Reader) readFrame() error {
	frameHeader := make([]byte, frameHeaderSize)
	if _, err := io.ReadFull(r.source, frameHeader); err != nil {
		return fmt.Errorf("%w: missing terminal frame", ErrInvalidStream)
	}
	index := binary.BigEndian.Uint64(frameHeader[:8])
	flags := frameHeader[8]
	plaintextSize := binary.BigEndian.Uint32(frameHeader[9:])
	if index != r.counter || flags > 1 || plaintextSize > ChunkSize || flags == 1 && plaintextSize != 0 || flags == 0 && plaintextSize == 0 {
		return fmt.Errorf("%w: invalid frame header", ErrInvalidStream)
	}
	ciphertext := make([]byte, int(plaintextSize)+r.aead.Overhead())
	if _, err := io.ReadFull(r.source, ciphertext); err != nil {
		return fmt.Errorf("%w: truncated frame", ErrInvalidStream)
	}
	nonce := frameNonce(r.noncePrefix, r.counter)
	plaintext, err := r.aead.Open(nil, nonce[:], ciphertext, frameAAD(r.headerHash, frameHeader))
	clear(ciphertext)
	if err != nil {
		return fmt.Errorf("%w: authenticate frame", ErrInvalidStream)
	}
	r.counter++
	if flags == 1 {
		var trailing [1]byte
		if count, err := r.source.Read(trailing[:]); count != 0 || err != io.EOF {
			clear(plaintext)
			return fmt.Errorf("%w: trailing bytes", ErrInvalidStream)
		}
		r.finished = true
		clear(plaintext)
		return nil
	}
	r.pending = plaintext
	return nil
}

func encodeHeaderPrefix(keyID string, noncePrefix [noncePrefixSize]byte, wrapNonce []byte) ([]byte, error) {
	if len(keyID) == 0 || len(keyID) > maxKeyIDBytes || len(wrapNonce) != 12 {
		return nil, ErrInvalidStream
	}
	header := make([]byte, 0, headerFixedBytes+len(keyID)+keySize+16)
	header = append(header, magic...)
	var version [2]byte
	binary.BigEndian.PutUint16(version[:], Version)
	header = append(header, version[:]...)
	header = append(header, byte(len(Algorithm)))
	header = append(header, Algorithm...)
	var chunk [4]byte
	binary.BigEndian.PutUint32(chunk[:], ChunkSize)
	header = append(header, chunk[:]...)
	header = append(header, noncePrefix[:]...)
	header = append(header, wrapNonce...)
	var keyIDSize [2]byte
	binary.BigEndian.PutUint16(keyIDSize[:], uint16(len(keyID)))
	header = append(header, keyIDSize[:]...)
	header = append(header, keyID...)
	return header, nil
}

func readHeader(source io.Reader) ([]byte, string, [noncePrefixSize]byte, []byte, []byte, error) {
	var noncePrefix [noncePrefixSize]byte
	fixed := make([]byte, len(magic)+2+1)
	if _, err := io.ReadFull(source, fixed); err != nil || !bytes.Equal(fixed[:len(magic)], magic) || binary.BigEndian.Uint16(fixed[len(magic):len(magic)+2]) != Version {
		return nil, "", noncePrefix, nil, nil, fmt.Errorf("%w: unsupported header", ErrInvalidStream)
	}
	algorithmSize := int(fixed[len(fixed)-1])
	if algorithmSize != len(Algorithm) {
		return nil, "", noncePrefix, nil, nil, fmt.Errorf("%w: unsupported algorithm", ErrInvalidStream)
	}
	restFixed := make([]byte, algorithmSize+4+noncePrefixSize+12+2)
	if _, err := io.ReadFull(source, restFixed); err != nil {
		return nil, "", noncePrefix, nil, nil, fmt.Errorf("%w: truncated header", ErrInvalidStream)
	}
	if string(restFixed[:algorithmSize]) != Algorithm || binary.BigEndian.Uint32(restFixed[algorithmSize:algorithmSize+4]) != ChunkSize {
		return nil, "", noncePrefix, nil, nil, fmt.Errorf("%w: unsupported parameters", ErrInvalidStream)
	}
	offset := algorithmSize + 4
	copy(noncePrefix[:], restFixed[offset:offset+noncePrefixSize])
	offset += noncePrefixSize
	wrapNonce := append([]byte(nil), restFixed[offset:offset+12]...)
	offset += 12
	keyIDSize := int(binary.BigEndian.Uint16(restFixed[offset : offset+2]))
	if keyIDSize < 1 || keyIDSize > maxKeyIDBytes {
		return nil, "", noncePrefix, nil, nil, fmt.Errorf("%w: invalid key id", ErrInvalidStream)
	}
	keyAndWrapped := make([]byte, keyIDSize+keySize+16)
	if _, err := io.ReadFull(source, keyAndWrapped); err != nil {
		return nil, "", noncePrefix, nil, nil, fmt.Errorf("%w: truncated wrapped key", ErrInvalidStream)
	}
	keyID := string(keyAndWrapped[:keyIDSize])
	wrappedDEK := append([]byte(nil), keyAndWrapped[keyIDSize:]...)
	header := make([]byte, 0, len(fixed)+len(restFixed)+len(keyAndWrapped))
	header = append(header, fixed...)
	header = append(header, restFixed...)
	header = append(header, keyAndWrapped...)
	clear(keyAndWrapped)
	return header, keyID, noncePrefix, wrapNonce, wrappedDEK, nil
}

func frameNonce(prefix [noncePrefixSize]byte, counter uint64) [12]byte {
	var nonce [12]byte
	copy(nonce[:noncePrefixSize], prefix[:])
	binary.BigEndian.PutUint64(nonce[noncePrefixSize:], counter)
	return nonce
}

func frameAAD(headerHash [sha256.Size]byte, frameHeader []byte) []byte {
	aad := make([]byte, 0, len(headerHash)+len(frameHeader))
	aad = append(aad, headerHash[:]...)
	return append(aad, frameHeader...)
}

func newAEAD(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

func writeFull(destination io.Writer, payload []byte) error {
	for len(payload) > 0 {
		count, err := destination.Write(payload)
		if err != nil {
			return err
		}
		if count <= 0 || count > len(payload) {
			return io.ErrShortWrite
		}
		payload = payload[count:]
	}
	return nil
}
