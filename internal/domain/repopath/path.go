package repopath

import (
	"errors"
	"path"
	"strings"
)

var ErrInvalid = errors.New("invalid repository-relative path")

// Normalize interprets repository paths with portable slash semantics rather
// than the host operating system's filepath rules.
func Normalize(value string, allowRoot bool) (string, error) {
	if value == "" || strings.IndexByte(value, 0) >= 0 {
		return "", ErrInvalid
	}

	portable := strings.ReplaceAll(value, `\`, "/")
	if strings.HasPrefix(portable, "/") || hasWindowsDrivePrefix(portable) {
		return "", ErrInvalid
	}

	clean := path.Clean(portable)
	if clean == ".." || strings.HasPrefix(clean, "../") || (!allowRoot && clean == ".") {
		return "", ErrInvalid
	}
	return clean, nil
}

func hasWindowsDrivePrefix(value string) bool {
	if len(value) < 2 || value[1] != ':' {
		return false
	}
	letter := value[0]
	return letter >= 'A' && letter <= 'Z' || letter >= 'a' && letter <= 'z'
}
