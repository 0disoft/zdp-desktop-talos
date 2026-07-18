package syncexchange

import (
	"context"
	"errors"
	"time"
)

const (
	MaxPackBytes      = 16 << 20
	MaxPacksPerRead   = 512
	MaxTotalReadBytes = 64 << 20
)

var (
	ErrInvalidRequest = errors.New("invalid sync folder exchange request")
	ErrUnsafePath     = errors.New("sync folder exchange path is unsafe")
	ErrPackConflict   = errors.New("sync folder pack conflicts with existing bytes")
	ErrReadLimit      = errors.New("sync folder read limit exceeded")
)

type PackDescriptor struct {
	VaultID       string
	DeviceID      string
	PackID        string
	SequenceStart uint64
	SequenceEnd   uint64
	CreatedAt     time.Time
}

type StoredPack struct {
	RelativePath  string
	SequenceStart uint64
	SequenceEnd   uint64
}

type ReadPack struct {
	StoredPack
	Encoded []byte
}

type Exchange interface {
	WritePack(context.Context, string, PackDescriptor, []byte) (StoredPack, bool, error)
	ReadPacks(context.Context, string, string, string) ([]ReadPack, error)
}
