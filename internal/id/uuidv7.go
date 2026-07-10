package id

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

var ErrInvalidUUIDTime = errors.New("uuidv7 time must not precede unix epoch")

func UUIDv7(now time.Time, random io.Reader) (string, error) {
	milliseconds := now.UnixMilli()
	if milliseconds < 0 {
		return "", ErrInvalidUUIDTime
	}

	var value [16]byte
	if _, err := io.ReadFull(random, value[:]); err != nil {
		return "", fmt.Errorf("read uuid randomness: %w", err)
	}

	var timestamp [8]byte
	binary.BigEndian.PutUint64(timestamp[:], uint64(milliseconds))
	copy(value[0:6], timestamp[2:8])
	value[6] = 0x70 | value[6]&0x0f
	value[8] = 0x80 | value[8]&0x3f

	return fmt.Sprintf(
		"%08x-%04x-%04x-%04x-%012x",
		value[0:4],
		value[4:6],
		value[6:8],
		value[8:10],
		value[10:16],
	), nil
}
