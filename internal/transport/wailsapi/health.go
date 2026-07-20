package wailsapi

import (
	"runtime"

	"github.com/0disoft/zdp-desktop-talos/internal/version"
	"github.com/0disoft/zdp-desktop-talos/internal/workeripc"
)

const ApplicationVersion = version.Application

type HealthSnapshot struct {
	Application     string `json:"application"`
	Version         string `json:"version"`
	Runtime         string `json:"runtime"`
	OperatingSystem string `json:"operating_system"`
	Architecture    string `json:"architecture"`
	WorkerProtocol  int    `json:"worker_protocol"`
	WorkerStatus    string `json:"worker_status"`
	VaultStatus     string `json:"vault_status"`
	LatestError     string `json:"latest_error,omitempty"`
}

type HealthService struct {
	vault           *VaultService
	workerAvailable bool
}

func NewHealthService(vault *VaultService, workerAvailable bool) *HealthService {
	return &HealthService{vault: vault, workerAvailable: workerAvailable}
}

func (s *HealthService) Snapshot() HealthSnapshot {
	workerStatus := "unavailable"
	vaultStatus := "unavailable"
	if s != nil {
		if s.workerAvailable {
			workerStatus = "ready"
		}
		if s.vault != nil {
			vaultStatus = s.vault.Status().State
		}
	}
	return HealthSnapshot{
		Application:     "Talos Agent",
		Version:         ApplicationVersion,
		Runtime:         runtime.Version(),
		OperatingSystem: runtime.GOOS,
		Architecture:    runtime.GOARCH,
		WorkerProtocol:  workeripc.ProtocolVersion,
		WorkerStatus:    workerStatus,
		VaultStatus:     vaultStatus,
	}
}
