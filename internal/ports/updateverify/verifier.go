package updateverify

import (
	"context"
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/domain/releaseupdate"
)

var (
	ErrInvalidRequest   = errors.New("update verification request is invalid")
	ErrUnsafeFile       = errors.New("update verification file is unsafe")
	ErrManifestTooLarge = errors.New("update manifest exceeds the size limit")
	ErrSignatureInvalid = errors.New("update manifest signature is invalid")
	ErrManifestInvalid  = errors.New("update manifest is invalid")
	ErrManifestExpired  = errors.New("update manifest is expired or not yet valid")
	ErrChannelMismatch  = errors.New("update manifest channel does not match")
	ErrTargetMismatch   = errors.New("update manifest target does not match")
	ErrVersionRejected  = errors.New("update version is not applicable")
	ErrArtifactChanged  = errors.New("update artifact changed during verification")
	ErrArtifactMismatch = errors.New("update artifact does not match the signed manifest")
)

type VerifyFilesInput struct {
	ManifestPath         string
	SignaturePath        string
	ArtifactPath         string
	ExpectedChannel      releaseupdate.Channel
	ExpectedOS           string
	ExpectedArchitecture string
	CurrentVersion       string
}

type Verifier interface {
	VerifyFiles(context.Context, VerifyFilesInput) (releaseupdate.VerifiedRelease, error)
}
