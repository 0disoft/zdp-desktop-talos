package wailsapi

import (
	"runtime"

	"github.com/0disoft/zdp-desktop-talos/internal/workeripc"
)

const ApplicationVersion = "0.1.7"

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

type HealthService struct{}

func (s *HealthService) Snapshot() HealthSnapshot {
	return HealthSnapshot{
		Application:     "Talos Agent",
		Version:         ApplicationVersion,
		Runtime:         runtime.Version(),
		OperatingSystem: runtime.GOOS,
		Architecture:    runtime.GOARCH,
		WorkerProtocol:  workeripc.ProtocolVersion,
		WorkerStatus:    "not_started",
		VaultStatus:     "locked",
	}
}
