package syncstate

import (
	"strings"
	"time"
)

type ReplayState string

const (
	ReplayApplied     ReplayState = "applied"
	ReplayConflicted  ReplayState = "conflicted"
	ReplayQuarantined ReplayState = "quarantined"
)

type ReplayItem struct {
	PackID     string
	VaultID    string
	DeviceID   string
	DeviceSeq  uint64
	EventID    string
	EventHash  string
	State      ReplayState
	ReasonCode string
	RecordedAt time.Time
}

func (i ReplayItem) Validate() error {
	if !validSHA256Reference(i.PackID) || strings.TrimSpace(i.VaultID) == "" || strings.TrimSpace(i.DeviceID) == "" || len(i.DeviceID) > MaxDeviceIDLength || i.DeviceSeq < 1 || strings.TrimSpace(i.EventID) == "" || len(i.EventID) > 128 || !validHexSHA256(i.EventHash) || !validReplayReason(i.ReasonCode) || i.RecordedAt.IsZero() {
		return ErrInvalidRecord
	}
	if i.State != ReplayApplied && i.State != ReplayConflicted && i.State != ReplayQuarantined {
		return ErrInvalidRecord
	}
	return nil
}

type ReplayBatch struct {
	PackID           string
	VaultID          string
	DeviceID         string
	SequenceStart    uint64
	SequenceEnd      uint64
	EventCount       int
	AppliedCount     int
	ConflictedCount  int
	QuarantinedCount int
	CompletedAt      time.Time
	LastEventID      string
}

func (b ReplayBatch) Validate() error {
	if !validSHA256Reference(b.PackID) || strings.TrimSpace(b.VaultID) == "" || strings.TrimSpace(b.DeviceID) == "" || len(b.DeviceID) > MaxDeviceIDLength || b.SequenceStart < 1 || b.SequenceEnd < b.SequenceStart || uint64(b.EventCount) != b.SequenceEnd-b.SequenceStart+1 || b.EventCount > MaxEventsPerPack || b.AppliedCount < 0 || b.ConflictedCount < 0 || b.QuarantinedCount < 0 || b.AppliedCount+b.ConflictedCount+b.QuarantinedCount != b.EventCount || b.CompletedAt.IsZero() || strings.TrimSpace(b.LastEventID) == "" {
		return ErrInvalidRecord
	}
	return nil
}

type ReplayResult struct {
	Batch ReplayBatch
	Items []ReplayItem
}

func (r ReplayResult) Validate() error {
	if r.Batch.Validate() != nil || len(r.Items) != r.Batch.EventCount {
		return ErrInvalidRecord
	}
	for index, item := range r.Items {
		if item.Validate() != nil || item.PackID != r.Batch.PackID || item.VaultID != r.Batch.VaultID || item.DeviceID != r.Batch.DeviceID || item.DeviceSeq != r.Batch.SequenceStart+uint64(index) {
			return ErrInvalidRecord
		}
	}
	return nil
}

func validReplayReason(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, candidate := range value {
		if candidate != '_' && candidate != '-' && (candidate < 'a' || candidate > 'z') && (candidate < '0' || candidate > '9') {
			return false
		}
	}
	return true
}
