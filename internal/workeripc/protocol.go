package workeripc

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
)

const (
	ProtocolVersion = 2
	MaxFrameSize    = 4 * 1024 * 1024
	maxHeaderSize   = 8 * 1024
)

var (
	ErrMalformedFrame  = errors.New("malformed worker frame")
	ErrFrameTooLarge   = errors.New("worker frame exceeds maximum size")
	ErrVersionMismatch = errors.New("worker protocol version mismatch")
)

type Message struct {
	Version   int             `json:"version"`
	Type      string          `json:"type"`
	RequestID string          `json:"request_id"`
	Payload   json.RawMessage `json:"payload,omitempty"`
	Error     *ErrorPayload   `json:"error,omitempty"`
}

type ErrorPayload struct {
	Code      string `json:"code"`
	Message   string `json:"message"`
	Retryable bool   `json:"retryable"`
}

type HandshakePayload struct {
	ProtocolVersion int      `json:"protocol_version"`
	WorkerVersion   string   `json:"worker_version,omitempty"`
	Capabilities    []string `json:"capabilities,omitempty"`
}

type Decoder struct {
	reader  *bufio.Reader
	maxSize int
}

type Encoder struct {
	writer  io.Writer
	maxSize int
}

func NewDecoder(reader io.Reader, maxSize int) *Decoder {
	if maxSize <= 0 {
		maxSize = MaxFrameSize
	}
	return &Decoder{reader: bufio.NewReader(reader), maxSize: maxSize}
}

func NewEncoder(writer io.Writer, maxSize int) *Encoder {
	if maxSize <= 0 {
		maxSize = MaxFrameSize
	}
	return &Encoder{writer: writer, maxSize: maxSize}
}

func (d *Decoder) Read() (Message, error) {
	contentLength := -1
	headerBytes := 0
	for {
		line, err := d.reader.ReadString('\n')
		if err != nil {
			return Message{}, err
		}
		headerBytes += len(line)
		if headerBytes > maxHeaderSize {
			return Message{}, fmt.Errorf("%w: headers too large", ErrMalformedFrame)
		}
		if !strings.HasSuffix(line, "\r\n") {
			return Message{}, fmt.Errorf("%w: headers require CRLF", ErrMalformedFrame)
		}
		line = strings.TrimSuffix(line, "\r\n")
		if line == "" {
			break
		}
		name, value, ok := strings.Cut(line, ":")
		if !ok {
			return Message{}, fmt.Errorf("%w: invalid header", ErrMalformedFrame)
		}
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "content-length":
			if contentLength >= 0 {
				return Message{}, fmt.Errorf("%w: duplicate content length", ErrMalformedFrame)
			}
			parsed, err := strconv.Atoi(strings.TrimSpace(value))
			if err != nil || parsed < 0 {
				return Message{}, fmt.Errorf("%w: invalid content length", ErrMalformedFrame)
			}
			contentLength = parsed
		case "content-type":
			if strings.TrimSpace(strings.ToLower(value)) != "application/json" {
				return Message{}, fmt.Errorf("%w: unsupported content type", ErrMalformedFrame)
			}
		default:
			return Message{}, fmt.Errorf("%w: unsupported header %q", ErrMalformedFrame, name)
		}
	}
	if contentLength < 0 {
		return Message{}, fmt.Errorf("%w: missing content length", ErrMalformedFrame)
	}
	if contentLength > d.maxSize {
		return Message{}, ErrFrameTooLarge
	}

	body := make([]byte, contentLength)
	if _, err := io.ReadFull(d.reader, body); err != nil {
		return Message{}, fmt.Errorf("%w: truncated body: %v", ErrMalformedFrame, err)
	}
	decoder := json.NewDecoder(bytes.NewReader(body))
	decoder.DisallowUnknownFields()
	var message Message
	if err := decoder.Decode(&message); err != nil {
		return Message{}, fmt.Errorf("%w: invalid JSON: %v", ErrMalformedFrame, err)
	}
	if decoder.Decode(&struct{}{}) != io.EOF {
		return Message{}, fmt.Errorf("%w: trailing JSON", ErrMalformedFrame)
	}
	if message.Version < 1 || message.Type == "" || message.RequestID == "" {
		return Message{}, fmt.Errorf("%w: version, type, and request_id are required", ErrMalformedFrame)
	}
	return message, nil
}

func (e *Encoder) Write(message Message) error {
	if message.Version < 1 || message.Type == "" || message.RequestID == "" {
		return fmt.Errorf("%w: version, type, and request_id are required", ErrMalformedFrame)
	}
	body, err := json.Marshal(message)
	if err != nil {
		return fmt.Errorf("encode worker message: %w", err)
	}
	if len(body) > e.maxSize {
		return ErrFrameTooLarge
	}
	header := fmt.Sprintf("Content-Length: %d\r\nContent-Type: application/json\r\n\r\n", len(body))
	if _, err := io.WriteString(e.writer, header); err != nil {
		return fmt.Errorf("write worker frame header: %w", err)
	}
	if _, err := e.writer.Write(body); err != nil {
		return fmt.Errorf("write worker frame body: %w", err)
	}
	return nil
}
