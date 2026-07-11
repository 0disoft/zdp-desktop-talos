package id

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"time"
)

var ErrInvalidUUIDTime = errors.New("uuidv7 time must not precede unix epoch")

func IsUUIDv7(value string) bool {
	if len(value) != 36 || value[8] != '-' || value[13] != '-' || value[18] != '-' || value[23] != '-' || value[14] != '7' {
		return false
	}
	for index, char := range value {
		if index == 8 || index == 13 || index == 18 || index == 23 {
			continue
		}
		if !isHex(char) {
			return false
		}
	}
	variant := value[19]
	return variant == '8' || variant == '9' || variant == 'a' || variant == 'b'
}

func isHex(value rune) bool {
	return value >= '0' && value <= '9' || value >= 'a' && value <= 'f'
}

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
