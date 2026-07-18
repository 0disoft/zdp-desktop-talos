package wailsapi

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/gitexchange"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncenrollment"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/syncstate"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/syncexchange"
)

type SyncDeviceView struct {
	DeviceID       string `json:"device_id"`
	KeyFingerprint string `json:"key_fingerprint"`
	State          string `json:"state"`
	Revision       int    `json:"revision"`
	NextSequence   string `json:"next_sequence"`
	Local          bool   `json:"local"`
	CreatedAt      string `json:"created_at"`
	UpdatedAt      string `json:"updated_at"`
}

type SyncEnrollmentView struct {
	EnrollmentID string `json:"enrollment_id"`
	Role         string `json:"role"`
	State        string `json:"state"`
	PeerDeviceID string `json:"peer_device_id,omitempty"`
	ExpiresAt    string `json:"expires_at"`
	UpdatedAt    string `json:"updated_at"`
}

type SyncReplayView struct {
	PackID           string   `json:"pack_id"`
	DeviceID         string   `json:"device_id"`
	SequenceStart    string   `json:"sequence_start"`
	SequenceEnd      string   `json:"sequence_end"`
	AppliedCount     int      `json:"applied_count"`
	ConflictedCount  int      `json:"conflicted_count"`
	QuarantinedCount int      `json:"quarantined_count"`
	ReasonCodes      []string `json:"reason_codes"`
	CompletedAt      string   `json:"completed_at"`
}

type SyncWorkspaceView struct {
	TaskID           string `json:"task_id"`
	WorkspaceID      string `json:"workspace_id"`
	BaselineCommit   string `json:"baseline_commit"`
	Mapped           bool   `json:"mapped"`
	LocalRoot        string `json:"local_root,omitempty"`
	State            string `json:"state,omitempty"`
	Revision         int    `json:"revision,omitempty"`
	VerifiedBaseline string `json:"verified_baseline,omitempty"`
}

type SyncOverview struct {
	Schema        string               `json:"schema"`
	Initialized   bool                 `json:"initialized"`
	LocalDeviceID string               `json:"local_device_id"`
	Devices       []SyncDeviceView     `json:"devices"`
	Enrollments   []SyncEnrollmentView `json:"enrollments"`
	Replays       []SyncReplayView     `json:"replays"`
	Workspaces    []SyncWorkspaceView  `json:"workspaces"`
}

type SyncOverviewResult struct {
	Overview *SyncOverview `json:"overview,omitempty"`
	Error    *TalosError   `json:"error,omitempty"`
}

type SyncAckResult struct {
	OK    bool        `json:"ok"`
	Error *TalosError `json:"error,omitempty"`
}

type EnrollmentOfferResult struct {
	EnrollmentID string      `json:"enrollment_id,omitempty"`
	Encoded      string      `json:"encoded,omitempty"`
	Secret       string      `json:"secret,omitempty"`
	ExpiresAt    string      `json:"expires_at,omitempty"`
	Error        *TalosError `json:"error,omitempty"`
}

type EnrollmentAcceptanceResult struct {
	EnrollmentID      string       `json:"enrollment_id,omitempty"`
	EncodedAcceptance string       `json:"encoded_acceptance,omitempty"`
	SourceDeviceID    string       `json:"source_device_id,omitempty"`
	TargetDeviceID    string       `json:"target_device_id,omitempty"`
	Replay            bool         `json:"replay"`
	Vault             *VaultStatus `json:"vault,omitempty"`
	Error             *TalosError  `json:"error,omitempty"`
}

type SyncFolderResult struct {
	RelativePath  string      `json:"relative_path,omitempty"`
	SequenceStart string      `json:"sequence_start,omitempty"`
	SequenceEnd   string      `json:"sequence_end,omitempty"`
	PackReplay    bool        `json:"pack_replay"`
	FileReplay    bool        `json:"file_replay"`
	Imported      int         `json:"imported"`
	Replayed      int         `json:"replayed"`
	Applied       int         `json:"applied"`
	Conflicted    int         `json:"conflicted"`
	Quarantined   int         `json:"quarantined"`
	Error         *TalosError `json:"error,omitempty"`
}

type SyncService struct {
	vault          *VaultService
	folderExchange syncexchange.Exchange
	gitExchange    syncexchange.Exchange
	inspector      repository.Inspector
	verifier       repository.BaselineVerifier
}

func NewSyncService(vault *VaultService, folderExchange, gitExchange syncexchange.Exchange, inspector repository.Inspector, verifier repository.BaselineVerifier) *SyncService {
	return &SyncService{vault: vault, folderExchange: folderExchange, gitExchange: gitExchange, inspector: inspector, verifier: verifier}
}

func (s *SyncService) Overview(correlationID string) SyncOverviewResult {
	if s == nil || s.vault == nil {
		return syncOverviewError(vaultbootstrap.ErrNotOpen, correlationID)
	}
	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.session == nil {
		return syncOverviewError(vaultbootstrap.ErrNotOpen, correlationID)
	}
	snapshot, err := s.vault.session.GetSyncSnapshot(context.Background())
	if err != nil {
		return syncOverviewError(err, correlationID)
	}
	overview := syncOverview(snapshot)
	return SyncOverviewResult{Overview: &overview}
}

func (s *SyncService) Initialize(correlationID string) SyncAckResult {
	return s.withOpenSession(correlationID, func(session *vaultbootstrap.Session) error {
		_, err := session.InitializeSync(context.Background())
		return err
	})
}

func (s *SyncService) CreateEnrollmentOffer(validMinutes int, correlationID string) EnrollmentOfferResult {
	if s == nil || s.vault == nil || validMinutes < int(syncenrollment.MinValidity/time.Minute) || validMinutes > int(syncenrollment.MaxValidity/time.Minute) {
		return enrollmentOfferError(vaultbootstrap.ErrInvalidInput, correlationID)
	}
	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.session == nil {
		return enrollmentOfferError(vaultbootstrap.ErrNotOpen, correlationID)
	}
	offer, err := s.vault.session.CreateEnrollmentOffer(context.Background(), vaultbootstrap.CreateEnrollmentOfferInput{ValidFor: time.Duration(validMinutes) * time.Minute})
	if err != nil {
		return enrollmentOfferError(err, correlationID)
	}
	return EnrollmentOfferResult{EnrollmentID: offer.EnrollmentID, Encoded: base64.RawStdEncoding.EncodeToString(offer.Encoded), Secret: offer.Secret, ExpiresAt: offer.ExpiresAt.UTC().Format(time.RFC3339Nano)}
}

func (s *SyncService) AcceptEnrollmentOffer(encoded, secret, correlationID string) EnrollmentAcceptanceResult {
	if s == nil || s.vault == nil || s.vault.creator == nil {
		return enrollmentAcceptanceError(vaultbootstrap.ErrNotOpen, correlationID)
	}
	decoded, err := decodeTransfer(encoded, syncenrollment.MaxPackageBytes)
	if err != nil || strings.TrimSpace(secret) == "" {
		return enrollmentAcceptanceError(vaultbootstrap.ErrInvalidInput, correlationID)
	}
	defer clear(decoded)
	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.session != nil {
		mapped := TalosError{Code: "VAULT_ALREADY_OPEN", Message: "가입 패키지를 받으려면 현재 Vault를 먼저 잠가 주세요.", CorrelationID: normalizeCorrelationID(correlationID)}
		return EnrollmentAcceptanceResult{Error: &mapped}
	}
	accepted, err := s.vault.creator.AcceptEnrollmentOffer(context.Background(), vaultbootstrap.AcceptEnrollmentOfferInput{Encoded: decoded, Secret: strings.TrimSpace(secret)})
	if err != nil {
		return enrollmentAcceptanceError(err, correlationID)
	}
	s.vault.session = accepted.Session
	status := s.vault.statusLocked()
	return EnrollmentAcceptanceResult{EnrollmentID: accepted.EnrollmentID, EncodedAcceptance: base64.RawStdEncoding.EncodeToString(accepted.EncodedAcceptance), SourceDeviceID: accepted.SourceDeviceID, TargetDeviceID: accepted.TargetDeviceID, Replay: accepted.Replay, Vault: &status}
}

func (s *SyncService) CompleteEnrollment(encodedAcceptance, secret, correlationID string) SyncAckResult {
	decoded, err := decodeTransfer(encodedAcceptance, syncenrollment.MaxPackageBytes)
	if err != nil || strings.TrimSpace(secret) == "" {
		return syncAckError(vaultbootstrap.ErrInvalidInput, correlationID)
	}
	defer clear(decoded)
	return s.withOpenSession(correlationID, func(session *vaultbootstrap.Session) error {
		_, err := session.CompleteEnrollment(context.Background(), vaultbootstrap.CompleteEnrollmentInput{EncodedAcceptance: decoded, Secret: strings.TrimSpace(secret)})
		return err
	})
}

func (s *SyncService) CancelEnrollment(enrollmentID, correlationID string) SyncAckResult {
	return s.withOpenSession(correlationID, func(session *vaultbootstrap.Session) error {
		_, err := session.CancelEnrollment(context.Background(), vaultbootstrap.CancelEnrollmentInput{EnrollmentID: strings.TrimSpace(enrollmentID)})
		return err
	})
}

func (s *SyncService) RevokeDevice(deviceID string, expectedRevision int, requestID, correlationID string) SyncAckResult {
	if normalizeCorrelationID(requestID) == "" {
		return syncAckError(vaultbootstrap.ErrInvalidInput, correlationID)
	}
	return s.withOpenSession(correlationID, func(session *vaultbootstrap.Session) error {
		_, err := session.RevokeSyncDevice(context.Background(), vaultbootstrap.RevokeSyncDeviceInput{DeviceID: strings.TrimSpace(deviceID), ExpectedRevision: expectedRevision, RequestID: requestID})
		return err
	})
}

func (s *SyncService) ExportFolder(root string, limit int, correlationID string) SyncFolderResult {
	if s == nil || s.folderExchange == nil || !validLocalPath(root) {
		return syncFolderError(syncexchange.ErrInvalidRequest, correlationID)
	}
	return s.exportExchange(s.folderExchange, root, limit, correlationID)
}

func (s *SyncService) ExportGit(root string, limit int, correlationID string) SyncFolderResult {
	if s == nil || s.gitExchange == nil {
		return syncFolderError(gitexchange.ErrUnavailable, correlationID)
	}
	if !validLocalPath(root) {
		return syncFolderError(syncexchange.ErrInvalidRequest, correlationID)
	}
	return s.exportExchange(s.gitExchange, root, limit, correlationID)
}

func (s *SyncService) exportExchange(exchange syncexchange.Exchange, root string, limit int, correlationID string) SyncFolderResult {
	var exported vaultbootstrap.ExportedSyncPackFile
	ack := s.withOpenSession(correlationID, func(session *vaultbootstrap.Session) error {
		var err error
		exported, err = session.ExportNextSyncPackToExchange(context.Background(), exchange, vaultbootstrap.ExportSyncPackToExchangeInput{Root: root, Limit: limit})
		return err
	})
	if ack.Error != nil {
		return SyncFolderResult{Error: ack.Error}
	}
	return SyncFolderResult{RelativePath: exported.File.RelativePath, SequenceStart: strconv.FormatUint(exported.File.SequenceStart, 10), SequenceEnd: strconv.FormatUint(exported.File.SequenceEnd, 10), PackReplay: exported.PackReplay, FileReplay: exported.FileReplay}
}

func (s *SyncService) ImportFolder(root, deviceID, correlationID string) SyncFolderResult {
	if s == nil || s.folderExchange == nil || !validLocalPath(root) || strings.TrimSpace(deviceID) == "" {
		return syncFolderError(syncexchange.ErrInvalidRequest, correlationID)
	}
	return s.importExchange(s.folderExchange, root, deviceID, correlationID)
}

func (s *SyncService) ImportGit(root, deviceID, correlationID string) SyncFolderResult {
	if s == nil || s.gitExchange == nil {
		return syncFolderError(gitexchange.ErrUnavailable, correlationID)
	}
	if !validLocalPath(root) || strings.TrimSpace(deviceID) == "" {
		return syncFolderError(syncexchange.ErrInvalidRequest, correlationID)
	}
	return s.importExchange(s.gitExchange, root, deviceID, correlationID)
}

func (s *SyncService) importExchange(exchange syncexchange.Exchange, root, deviceID, correlationID string) SyncFolderResult {
	var imported []vaultbootstrap.ImportedSyncPackFile
	ack := s.withOpenSession(correlationID, func(session *vaultbootstrap.Session) error {
		var err error
		imported, err = session.ImportSyncPacksFromExchange(context.Background(), exchange, vaultbootstrap.ImportSyncPacksFromExchangeInput{Root: root, DeviceID: strings.TrimSpace(deviceID), ReceivedAt: time.Now().UTC()})
		return err
	})
	if ack.Error != nil {
		return SyncFolderResult{Error: ack.Error}
	}
	result := SyncFolderResult{Imported: len(imported)}
	for _, item := range imported {
		if item.Result.Replayed {
			result.Replayed++
		}
		result.Applied += item.Result.Replay.Batch.AppliedCount
		result.Conflicted += item.Result.Replay.Batch.ConflictedCount
		result.Quarantined += item.Result.Replay.Batch.QuarantinedCount
	}
	return result
}

func (s *SyncService) MapWorkspace(taskID, localPath string, expectedRevision int, requestID, correlationID string) SyncAckResult {
	if s == nil || s.inspector == nil || s.verifier == nil || !validLocalPath(localPath) || normalizeCorrelationID(requestID) == "" {
		return syncAckError(vaultbootstrap.ErrInvalidInput, correlationID)
	}
	return s.withOpenSession(correlationID, func(session *vaultbootstrap.Session) error {
		_, err := session.MapTaskWorkspace(context.Background(), s.inspector, s.verifier, vaultbootstrap.MapTaskWorkspaceInput{TaskID: strings.TrimSpace(taskID), LocalPath: strings.TrimSpace(localPath), ExpectedRevision: expectedRevision, RequestID: requestID})
		return err
	})
}

func (s *SyncService) RevokeWorkspace(workspaceID string, expectedRevision int, requestID, correlationID string) SyncAckResult {
	if normalizeCorrelationID(requestID) == "" {
		return syncAckError(vaultbootstrap.ErrInvalidInput, correlationID)
	}
	return s.withOpenSession(correlationID, func(session *vaultbootstrap.Session) error {
		_, err := session.RevokeTaskWorkspace(context.Background(), vaultbootstrap.RevokeTaskWorkspaceInput{WorkspaceID: strings.TrimSpace(workspaceID), ExpectedRevision: expectedRevision, RequestID: requestID})
		return err
	})
}

func (s *SyncService) withOpenSession(correlationID string, action func(*vaultbootstrap.Session) error) SyncAckResult {
	if s == nil || s.vault == nil || action == nil {
		return syncAckError(vaultbootstrap.ErrNotOpen, correlationID)
	}
	s.vault.mu.Lock()
	defer s.vault.mu.Unlock()
	if s.vault.session == nil {
		return syncAckError(vaultbootstrap.ErrNotOpen, correlationID)
	}
	if err := action(s.vault.session); err != nil {
		return syncAckError(err, correlationID)
	}
	return SyncAckResult{OK: true}
}

func syncOverview(snapshot vaultbootstrap.SyncSnapshot) SyncOverview {
	result := SyncOverview{Schema: "talos.sync-overview/1", Initialized: snapshot.Initialized, LocalDeviceID: snapshot.LocalDevice.DeviceID, Devices: make([]SyncDeviceView, 0, len(snapshot.Devices)), Enrollments: make([]SyncEnrollmentView, 0, len(snapshot.Enrollments)), Replays: make([]SyncReplayView, 0, len(snapshot.Replays)), Workspaces: make([]SyncWorkspaceView, 0, len(snapshot.Workspaces))}
	if snapshot.Initialized {
		result.Devices = append(result.Devices, syncDeviceView(snapshot.LocalDevice, true))
	}
	for _, device := range snapshot.Devices {
		if snapshot.Initialized && device.DeviceID == snapshot.LocalDevice.DeviceID {
			continue
		}
		if len(result.Devices) == 100 {
			break
		}
		result.Devices = append(result.Devices, syncDeviceView(device, false))
	}
	for _, enrollment := range snapshot.Enrollments {
		result.Enrollments = append(result.Enrollments, SyncEnrollmentView{EnrollmentID: enrollment.EnrollmentID, Role: string(enrollment.Role), State: string(enrollment.State), PeerDeviceID: enrollment.PeerDeviceID, ExpiresAt: enrollment.ExpiresAt.UTC().Format(time.RFC3339Nano), UpdatedAt: enrollment.UpdatedAt.UTC().Format(time.RFC3339Nano)})
	}
	for _, replay := range snapshot.Replays {
		reasons := make([]string, 0, 8)
		seen := map[string]bool{}
		for _, item := range replay.Items {
			if item.State == syncstate.ReplayApplied || seen[item.ReasonCode] || len(reasons) == 8 {
				continue
			}
			seen[item.ReasonCode] = true
			reasons = append(reasons, item.ReasonCode)
		}
		result.Replays = append(result.Replays, SyncReplayView{PackID: replay.Batch.PackID, DeviceID: replay.Batch.DeviceID, SequenceStart: strconv.FormatUint(replay.Batch.SequenceStart, 10), SequenceEnd: strconv.FormatUint(replay.Batch.SequenceEnd, 10), AppliedCount: replay.Batch.AppliedCount, ConflictedCount: replay.Batch.ConflictedCount, QuarantinedCount: replay.Batch.QuarantinedCount, ReasonCodes: reasons, CompletedAt: replay.Batch.CompletedAt.UTC().Format(time.RFC3339Nano)})
	}
	for _, taskWorkspace := range snapshot.Workspaces {
		requirement := taskWorkspace.Requirement
		view := SyncWorkspaceView{TaskID: taskWorkspace.TaskID, WorkspaceID: requirement.WorkspaceID, BaselineCommit: requirement.BaselineCommit, Mapped: requirement.Mapped}
		if requirement.Mapped {
			view.LocalRoot = requirement.Mapping.LocalRoot
			view.State = string(requirement.Mapping.State)
			view.Revision = requirement.Mapping.Revision
			view.VerifiedBaseline = requirement.Mapping.VerifiedBaseline
		}
		result.Workspaces = append(result.Workspaces, view)
	}
	return result
}

func syncDeviceView(device syncstate.Device, local bool) SyncDeviceView {
	fingerprint := sha256.Sum256(device.PublicKey)
	return SyncDeviceView{DeviceID: device.DeviceID, KeyFingerprint: hex.EncodeToString(fingerprint[:]), State: string(device.State), Revision: device.Revision, NextSequence: strconv.FormatUint(device.NextSequence, 10), Local: local, CreatedAt: device.CreatedAt.UTC().Format(time.RFC3339Nano), UpdatedAt: device.UpdatedAt.UTC().Format(time.RFC3339Nano)}
}

func decodeTransfer(value string, maxBytes int) ([]byte, error) {
	if value == "" || len(value) > base64.RawStdEncoding.EncodedLen(maxBytes) {
		return nil, vaultbootstrap.ErrInvalidInput
	}
	decoded, err := base64.RawStdEncoding.DecodeString(value)
	if err != nil || len(decoded) == 0 || len(decoded) > maxBytes {
		clear(decoded)
		return nil, vaultbootstrap.ErrInvalidInput
	}
	return decoded, nil
}

func validLocalPath(value string) bool {
	return strings.TrimSpace(value) != "" && len(value) <= 32767 && !strings.ContainsRune(value, '\x00')
}

func syncOverviewError(err error, correlationID string) SyncOverviewResult {
	mapped := MapError(err, correlationID)
	return SyncOverviewResult{Error: &mapped}
}

func syncAckError(err error, correlationID string) SyncAckResult {
	mapped := MapError(err, correlationID)
	return SyncAckResult{Error: &mapped}
}

func enrollmentOfferError(err error, correlationID string) EnrollmentOfferResult {
	mapped := MapError(err, correlationID)
	return EnrollmentOfferResult{Error: &mapped}
}

func enrollmentAcceptanceError(err error, correlationID string) EnrollmentAcceptanceResult {
	mapped := MapError(err, correlationID)
	return EnrollmentAcceptanceResult{Error: &mapped}
}

func syncFolderError(err error, correlationID string) SyncFolderResult {
	mapped := MapError(err, correlationID)
	return SyncFolderResult{Error: &mapped}
}
