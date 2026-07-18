package bootstrap

import (
	"crypto/ed25519"
	"fmt"
	"path/filepath"
	"runtime"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/dpapikeyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/protectedupdate"
	"github.com/0disoft/zdp-desktop-talos/internal/adapters/signedupdate"
	"github.com/0disoft/zdp-desktop-talos/internal/application/updatepreparation"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/releaseupdate"
	"github.com/0disoft/zdp-desktop-talos/internal/version"
)

// NewUpdatePreparationService assembles the dormant local update gate. The
// caller must provide a publisher key compiled from a reviewed release-key
// source. User input, environment variables, and update manifests are not
// trusted key sources.
func NewUpdatePreparationService(localDataRoot string, publisherKey ed25519.PublicKey, channel releaseupdate.Channel) (*updatepreparation.Service, error) {
	if localDataRoot == "" || len(publisherKey) != ed25519.PublicKeySize || !channel.Valid() {
		return nil, updatepreparation.ErrInvalidRequest
	}
	keys, err := dpapikeyvault.Open(filepath.Join(localDataRoot, "keys"))
	if err != nil {
		return nil, fmt.Errorf("initialize protected update preparation storage: %w", err)
	}
	journal, err := protectedupdate.New(keys)
	if err != nil {
		return nil, fmt.Errorf("initialize protected update preparation journal: %w", err)
	}
	verifier, err := signedupdate.New(publisherKey, time.Now)
	if err != nil {
		return nil, fmt.Errorf("initialize signed update verifier: %w", err)
	}
	service, err := updatepreparation.NewDefault(verifier, journal, updatepreparation.Policy{Channel: channel, OS: runtime.GOOS, Architecture: runtime.GOARCH, Version: version.Application})
	if err != nil {
		return nil, fmt.Errorf("initialize update preparation service: %w", err)
	}
	return service, nil
}
