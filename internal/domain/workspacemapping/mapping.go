package workspacemapping

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"strings"
	"time"
)

const (
	workspaceIDPrefix = "workspace-v1-"
	MaxWorkspaceIDLen = len(workspaceIDPrefix) + sha256.Size*2
)

var ErrInvalidRecord = errors.New("invalid workspace mapping record")

type State string

const (
	StateActive  State = "active"
	StateRevoked State = "revoked"
)

type Record struct {
	WorkspaceID         string
	VaultID             string
	SourceWorkspaceHash string
	LocalRoot           string
	LocalRootHash       string
	VerifiedBaseline    string
	State               State
	Revision            int
	CreatedAt           time.Time
	UpdatedAt           time.Time
	CreatedEventID      string
	LastEventID         string
}

func (r Record) Validate() error {
	if !ValidWorkspaceID(r.WorkspaceID) || strings.TrimSpace(r.VaultID) == "" || !validHash(r.SourceWorkspaceHash) || !validHash(r.LocalRootHash) || !validCommit(r.VerifiedBaseline) || !filepath.IsAbs(r.LocalRoot) || RootHash(r.LocalRoot) != r.LocalRootHash || r.Revision < 1 || r.CreatedAt.IsZero() || r.UpdatedAt.Before(r.CreatedAt) || r.CreatedEventID == "" || r.LastEventID == "" {
		return ErrInvalidRecord
	}
	if r.WorkspaceID != ID(r.VaultID, r.SourceWorkspaceHash) || (r.State != StateActive && r.State != StateRevoked) {
		return ErrInvalidRecord
	}
	return nil
}

type Requirement struct {
	WorkspaceID         string
	VaultID             string
	SourceWorkspaceHash string
	BaselineCommit      string
	Mapped              bool
	Mapping             Record
}

func (r Requirement) Validate() error {
	if !ValidWorkspaceID(r.WorkspaceID) || strings.TrimSpace(r.VaultID) == "" || !validHash(r.SourceWorkspaceHash) || !validCommit(r.BaselineCommit) || r.WorkspaceID != ID(r.VaultID, r.SourceWorkspaceHash) {
		return ErrInvalidRecord
	}
	if r.Mapped {
		if r.Mapping.Validate() != nil || r.Mapping.WorkspaceID != r.WorkspaceID || r.Mapping.VaultID != r.VaultID || r.Mapping.State != StateActive {
			return ErrInvalidRecord
		}
	} else if r.Mapping.WorkspaceID != "" {
		return ErrInvalidRecord
	}
	return nil
}

func ID(vaultID, sourceWorkspaceHash string) string {
	hash := sha256.Sum256([]byte("talos.workspace/v1\x00" + vaultID + "\x00" + sourceWorkspaceHash))
	return workspaceIDPrefix + hex.EncodeToString(hash[:])
}

func ValidWorkspaceID(value string) bool {
	if !strings.HasPrefix(value, workspaceIDPrefix) || len(value) != MaxWorkspaceIDLen {
		return false
	}
	return validHash(strings.TrimPrefix(value, workspaceIDPrefix))
}

func RootHash(root string) string {
	clean := filepath.Clean(root)
	hash := sha256.Sum256([]byte(clean))
	return hex.EncodeToString(hash[:])
}

func validHash(value string) bool {
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded) == sha256.Size && hex.EncodeToString(decoded) == value
}

func validCommit(value string) bool {
	if len(value) < 40 || len(value) > 64 {
		return false
	}
	decoded, err := hex.DecodeString(value)
	return err == nil && len(decoded)*2 == len(value) && hex.EncodeToString(decoded) == value
}
