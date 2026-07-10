package wailsapi

import (
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
	"github.com/0disoft/zdp-desktop-talos/internal/workeripc"
)

type TalosError struct {
	Code          string            `json:"code"`
	Message       string            `json:"message"`
	Retryable     bool              `json:"retryable"`
	CorrelationID string            `json:"correlation_id"`
	Details       map[string]string `json:"details,omitempty"`
}

func MapError(err error, correlationID string) TalosError {
	mapped := TalosError{
		Code:          "INTERNAL_ERROR",
		Message:       "요청을 완료하지 못했습니다.",
		Retryable:     false,
		CorrelationID: correlationID,
	}
	switch {
	case errors.Is(err, event.ErrSecretPayload):
		mapped.Code = "SECRET_PAYLOAD_REJECTED"
		mapped.Message = "비밀정보는 Vault 이벤트로 저장할 수 없습니다."
	case errors.Is(err, sqliteevent.ErrNotFound):
		mapped.Code = "EVENT_NOT_FOUND"
		mapped.Message = "요청한 이벤트를 찾을 수 없습니다."
	case errors.Is(err, envelope.ErrInvalidEnvelope):
		mapped.Code = "ENCRYPTED_PAYLOAD_INVALID"
		mapped.Message = "암호화된 데이터를 검증할 수 없습니다."
	case errors.Is(err, workeripc.ErrVersionMismatch):
		mapped.Code = "WORKER_PROTOCOL_VERSION_MISMATCH"
		mapped.Message = "Worker와 앱의 프로토콜 버전이 다릅니다."
	}
	return mapped
}
