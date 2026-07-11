package wailsapi

import (
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
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
		CorrelationID: normalizeCorrelationID(correlationID),
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
	case errors.Is(err, vaultbootstrap.ErrCompensationFailed):
		mapped.Code = "VAULT_CLEANUP_INCOMPLETE"
		mapped.Message = "Vault 생성에 실패했고 일부 로컬 데이터를 자동으로 정리하지 못했습니다."
	case errors.Is(err, vaultbootstrap.ErrPurgeIncomplete):
		mapped.Code = "VAULT_PURGE_INCOMPLETE"
		mapped.Message = "Vault 키 파기 또는 로컬 데이터 정리가 완료되지 않았습니다. 다음 시작 때 안전하게 다시 시도합니다."
	case errors.Is(err, vaultbootstrap.ErrInvalidInput), errors.Is(err, vaultstore.ErrInvalidCommand):
		mapped.Code = "VAULT_INPUT_INVALID"
		mapped.Message = "Vault 설정값을 확인해 주세요."
	case errors.Is(err, vaultstore.ErrAlreadyExists):
		mapped.Code = "VAULT_ALREADY_EXISTS"
		mapped.Message = "같은 Vault가 이미 존재합니다."
	case errors.Is(err, vaultstore.ErrRevisionConflict):
		mapped.Code = "VAULT_REVISION_CONFLICT"
		mapped.Message = "Vault가 다른 요청에서 변경되었습니다. 최신 상태를 다시 확인해 주세요."
	case errors.Is(err, vaultstore.ErrIdempotencyConflict), errors.Is(err, vaultstore.ErrIdempotencyUnverified):
		mapped.Code = "VAULT_REQUEST_CONFLICT"
		mapped.Message = "같은 요청 식별자가 다른 변경에 사용되었습니다."
	case errors.Is(err, vaultcatalog.ErrCorrupt):
		mapped.Code = "VAULT_CATALOG_INVALID"
		mapped.Message = "보호된 Vault 목록을 검증할 수 없습니다."
	case errors.Is(err, vaultbootstrap.ErrNotCataloged), errors.Is(err, vaultcatalog.ErrNotFound):
		mapped.Code = "VAULT_NOT_FOUND"
		mapped.Message = "요청한 Vault를 찾을 수 없습니다."
	case errors.Is(err, keyvault.ErrNotFound), errors.Is(err, keyvault.ErrCorrupt), errors.Is(err, keyvault.ErrUnseal):
		mapped.Code = "VAULT_OPEN_FAILED"
		mapped.Message = "Vault 키 또는 로컬 데이터를 열 수 없습니다."
	case errors.Is(err, vaultstore.ErrNotFound):
		mapped.Code = "VAULT_OPEN_FAILED"
		mapped.Message = "Vault 로컬 상태를 열 수 없습니다."
	case errors.Is(err, workeripc.ErrVersionMismatch):
		mapped.Code = "WORKER_PROTOCOL_VERSION_MISMATCH"
		mapped.Message = "Worker와 앱의 프로토콜 버전이 다릅니다."
	}
	return mapped
}

func normalizeCorrelationID(value string) string {
	if len(value) == 0 || len(value) > 128 {
		return ""
	}
	for _, char := range value {
		if char >= 'a' && char <= 'z' || char >= 'A' && char <= 'Z' || char >= '0' && char <= '9' || char == '-' || char == '_' || char == '.' || char == ':' {
			continue
		}
		return ""
	}
	return value
}
