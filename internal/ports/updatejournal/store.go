package updatejournal

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/releaseupdate"
)

const ProtectedRecordKeyID = "update-preparation-v1"

var (
	ErrInvalidRecord = errors.New("update preparation record is invalid")
	ErrNotFound      = errors.New("update preparation record was not found")
	ErrCorrupt       = errors.New("update preparation record is corrupt")
)

type Store interface {
	Save(context.Context, releaseupdate.Preparation) error
	Load(context.Context, string) (releaseupdate.Preparation, error)
	Delete(context.Context, string) error
}
