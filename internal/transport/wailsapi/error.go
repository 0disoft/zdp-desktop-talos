package wailsapi

import (
	"errors"

	"github.com/0disoft/zdp-desktop-talos/internal/adapters/sqliteevent"
	"github.com/0disoft/zdp-desktop-talos/internal/application/contextassembly"
	"github.com/0disoft/zdp-desktop-talos/internal/application/executionruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/memorycompile"
	"github.com/0disoft/zdp-desktop-talos/internal/application/memorykernel"
	"github.com/0disoft/zdp-desktop-talos/internal/application/memoryprojection"
	"github.com/0disoft/zdp-desktop-talos/internal/application/modelruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/application/patchcommand"
	"github.com/0disoft/zdp-desktop-talos/internal/application/patchreview"
	"github.com/0disoft/zdp-desktop-talos/internal/application/permissionreview"
	"github.com/0disoft/zdp-desktop-talos/internal/application/vaultbootstrap"
	"github.com/0disoft/zdp-desktop-talos/internal/domain/event"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/decisionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/executionstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/keyvault"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/memorystore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/modelprovider"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/patchstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/repository"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/taskstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultcatalog"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/vaultstore"
	"github.com/0disoft/zdp-desktop-talos/internal/ports/workerruntime"
	"github.com/0disoft/zdp-desktop-talos/internal/security/envelope"
	"github.com/0disoft/zdp-desktop-talos/internal/workeripc"
)

var (
	errExecutionUnavailable    = errors.New("execution runtime is unavailable")
	errPatchReviewUnavailable  = errors.New("patch review runtime is unavailable")
	errPatchCommandUnavailable = errors.New("patch command runtime is unavailable")
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
	case errors.Is(err, memoryprojection.ErrSecretFindings):
		mapped.Code = "PROJECTION_SECRET_FINDINGS"
		mapped.Message = "비밀정보로 보이는 내용이 있어 projection을 만들지 않았습니다."
	case errors.Is(err, memoryprojection.ErrOutputLimit):
		mapped.Code = "PROJECTION_OUTPUT_LIMIT"
		mapped.Message = "Projection 결과가 안전한 크기 제한을 넘었습니다."
	case errors.Is(err, memoryprojection.ErrInvalidRequest):
		mapped.Code = "PROJECTION_REQUEST_INVALID"
		mapped.Message = "Projection에 포함할 기억 상태를 확인해 주세요."
	case errors.Is(err, errModelConsentRequired):
		mapped.Code = "MODEL_EGRESS_CONSENT_REQUIRED"
		mapped.Message = "현재 Workspace와 모델에 대한 외부 전송 내용을 확인해 주세요."
	case errors.Is(err, errModelConfigurationChanged):
		mapped.Code = "MODEL_CONFIGURATION_CHANGED"
		mapped.Message = "모델 또는 Workspace 기준점이 바뀌었습니다. 전송 내용을 다시 확인해 주세요."
	case errors.Is(err, errModelUnavailable), errors.Is(err, modelprovider.ErrUnavailable):
		mapped.Code = "MODEL_PROVIDER_UNAVAILABLE"
		mapped.Message = "모델 제공자 설정 또는 연결을 사용할 수 없습니다. 로컬 기능은 계속 사용할 수 있습니다."
		mapped.Retryable = true
	case errors.Is(err, modelprovider.ErrRateLimited):
		mapped.Code = "MODEL_PROVIDER_RATE_LIMITED"
		mapped.Message = "모델 제공자의 사용량 제한에 도달했습니다. 나중에 다시 시도해 주세요."
		mapped.Retryable = true
	case errors.Is(err, modelprovider.ErrTimeout):
		mapped.Code = "MODEL_PROVIDER_TIMEOUT"
		mapped.Message = "모델 응답 시간이 초과되었습니다. 전송 기록은 실패 상태로 남겼습니다."
		mapped.Retryable = true
	case errors.Is(err, modelruntime.ErrEgressBlocked):
		mapped.Code = "MODEL_EGRESS_BLOCKED"
		mapped.Message = "민감도 또는 크기 제한 때문에 모델로 내용을 보내지 않았습니다."
	case errors.Is(err, modelruntime.ErrPlanRejected), errors.Is(err, modelprovider.ErrInvalidResponse):
		mapped.Code = "MODEL_PLAN_REJECTED"
		mapped.Message = "모델 응답이 Task Contract의 계획 형식을 만족하지 않아 실행하지 않았습니다."
	case errors.Is(err, modelruntime.ErrReceiptFailed):
		mapped.Code = "MODEL_RECEIPT_FAILED"
		mapped.Message = "외부 전송 기록을 안전하게 저장하지 못해 모델 요청을 완료하지 않았습니다."
	case errors.Is(err, modelruntime.ErrMemoryContextFailed):
		mapped.Code = "MODEL_MEMORY_CONTEXT_FAILED"
		mapped.Message = "현재 Task에 적용할 기억을 안전하게 조립하지 못했습니다."
	case errors.Is(err, modelruntime.ErrInvalidRequest):
		mapped.Code = "MODEL_REQUEST_INVALID"
		mapped.Message = "Task, Workspace 또는 모델 전송 요청을 다시 확인해 주세요."
	case errors.Is(err, memorycompile.ErrSensitiveSource):
		mapped.Code = "MEMORY_SECRET_REJECTED"
		mapped.Message = "비밀정보로 보이는 내용이 있어 기억 후보를 만들지 않았습니다."
	case errors.Is(err, memorycompile.ErrInvalidRequest), errors.Is(err, memorycompile.ErrInvalidSource):
		mapped.Code = "MEMORY_COMPILATION_INVALID"
		mapped.Message = "현재 Task와 Decision 출처에서 기억 후보를 안전하게 만들 수 없습니다."
	case errors.Is(err, contextassembly.ErrInvalidRequest):
		mapped.Code = "MEMORY_CONTEXT_INVALID"
		mapped.Message = "현재 Task에 적용할 기억 범위를 확인해 주세요."
	case errors.Is(err, memorykernel.ErrReviewRequired):
		mapped.Code = "MEMORY_REVIEW_REQUIRED"
		mapped.Message = "기억 후보의 승인, 거절 또는 격리 결과를 선택해 주세요."
	case errors.Is(err, memorykernel.ErrInvalidRequest), errors.Is(err, memorystore.ErrInvalidCommand):
		mapped.Code = "MEMORY_REQUEST_INVALID"
		mapped.Message = "기억 후보와 검토 요청을 확인해 주세요."
	case errors.Is(err, memorystore.ErrRevisionConflict):
		mapped.Code = "MEMORY_REVISION_CONFLICT"
		mapped.Message = "기억 후보가 다른 검토에서 변경되었습니다. 최신 상태를 다시 확인해 주세요."
	case errors.Is(err, memorystore.ErrTransitionRejected):
		mapped.Code = "MEMORY_TRANSITION_REJECTED"
		mapped.Message = "현재 상태에서는 요청한 기억 검토 결과를 적용할 수 없습니다."
	case errors.Is(err, memorystore.ErrEvidenceNotFound):
		mapped.Code = "MEMORY_EVIDENCE_NOT_FOUND"
		mapped.Message = "기억 후보의 Vault 출처 이벤트를 확인할 수 없습니다."
	case errors.Is(err, memorystore.ErrIdempotencyConflict), errors.Is(err, memorystore.ErrIdempotencyUnverified):
		mapped.Code = "MEMORY_REQUEST_CONFLICT"
		mapped.Message = "같은 요청 식별자가 다른 기억 작업에 사용되었습니다."
	case errors.Is(err, memorystore.ErrNotFound):
		mapped.Code = "MEMORY_NOT_FOUND"
		mapped.Message = "요청한 기억 후보를 찾을 수 없습니다."
	case errors.Is(err, errPatchCommandUnavailable):
		mapped.Code = "PATCH_COMMAND_UNAVAILABLE"
		mapped.Message = "이 기기에서 패치를 안전하게 처리할 수 없습니다."
	case errors.Is(err, patchcommand.ErrInvalidRequest), errors.Is(err, patchstore.ErrInvalidCommand):
		mapped.Code = "PATCH_COMMAND_INVALID"
		mapped.Message = "패치 요청과 최신 검토 상태를 확인해 주세요."
	case errors.Is(err, patchcommand.ErrStaleReview):
		mapped.Code = "PATCH_REVIEW_STALE"
		mapped.Message = "패치 또는 Task Contract가 바뀌었습니다. 다시 검증해 주세요."
	case errors.Is(err, patchcommand.ErrSecretFindings):
		mapped.Code = "PATCH_SECRET_FINDINGS"
		mapped.Message = "비밀정보로 보이는 내용이 있어 패치를 적용하지 않았습니다."
	case errors.Is(err, patchcommand.ErrScopeViolation):
		mapped.Code = "PATCH_SCOPE_VIOLATION"
		mapped.Message = "Task Contract 범위를 벗어난 파일이 있어 패치를 적용하지 않았습니다."
	case errors.Is(err, patchcommand.ErrNoChanges):
		mapped.Code = "PATCH_EMPTY"
		mapped.Message = "적용할 변경이 없습니다."
	case errors.Is(err, patchstore.ErrBlockingDecision):
		mapped.Code = "PATCH_BLOCKING_DECISION"
		mapped.Message = "먼저 답해야 하는 Decision이 남아 있습니다."
	case errors.Is(err, patchcommand.ErrPatchConflict), errors.Is(err, patchstore.ErrConflict):
		mapped.Code = "PATCH_CONFLICT"
		mapped.Message = "저장소 또는 패치 상태가 바뀌었습니다. 상태를 다시 확인해 주세요."
	case errors.Is(err, patchcommand.ErrPatchApplyFailed):
		mapped.Code = "PATCH_APPLY_FAILED"
		mapped.Message = "현재 기본 작업 폴더에 패치를 적용할 수 없습니다."
	case errors.Is(err, patchcommand.ErrActionUnresolved), errors.Is(err, patchcommand.ErrPatchOutcomeUnknown):
		mapped.Code = "PATCH_OUTCOME_UNKNOWN"
		mapped.Message = "패치 처리 결과를 확정할 수 없습니다. 저장소 상태를 직접 확인해 주세요."
	case errors.Is(err, patchstore.ErrIdempotencyConflict):
		mapped.Code = "PATCH_REQUEST_CONFLICT"
		mapped.Message = "같은 요청 식별자가 다른 패치 작업에 사용되었습니다."
	case errors.Is(err, errPatchReviewUnavailable):
		mapped.Code = "PATCH_REVIEW_UNAVAILABLE"
		mapped.Message = "이 기기에서 패치 상태를 안전하게 확인할 수 없습니다."
	case errors.Is(err, patchreview.ErrInvalidRequest):
		mapped.Code = "PATCH_REVIEW_INVALID"
		mapped.Message = "검토할 Task를 확인해 주세요."
	case errors.Is(err, repository.ErrWorktreeNotFound):
		mapped.Code = "PATCH_REVIEW_NOT_READY"
		mapped.Message = "아직 검토할 패치가 없습니다. 검증을 먼저 실행해 주세요."
	case errors.Is(err, repository.ErrWorktreeOwnership), errors.Is(err, repository.ErrWorktreeSnapshotFailed):
		mapped.Code = "PATCH_REVIEW_UNAVAILABLE"
		mapped.Message = "현재 패치 상태를 안전하게 확인하지 못했습니다."
	case errors.Is(err, errExecutionUnavailable), errors.Is(err, workerruntime.ErrUnavailable):
		mapped.Code = "EXECUTION_UNAVAILABLE"
		mapped.Message = "이 기기에서 안전한 검증 실행기를 사용할 수 없습니다."
	case errors.Is(err, executionruntime.ErrInvalidRequest):
		mapped.Code = "EXECUTION_REQUEST_INVALID"
		mapped.Message = "현재 Task Contract의 검증 요청을 확인해 주세요."
	case errors.Is(err, executionruntime.ErrPermissionDenied):
		mapped.Code = "EXECUTION_PERMISSION_DENIED"
		mapped.Message = "현재 Task Contract 또는 권한 정책이 이 검증을 허용하지 않습니다."
	case errors.Is(err, executionruntime.ErrEvidenceUnavailable):
		mapped.Code = "EXECUTION_EVIDENCE_UNAVAILABLE"
		mapped.Message = "현재 코드 상태에 검증 결과를 안전하게 연결하지 못했습니다."
	case errors.Is(err, executionruntime.ErrJournalFailed):
		mapped.Code = "EXECUTION_JOURNAL_FAILED"
		mapped.Message = "검증 실행 기록을 안전하게 저장하지 못했습니다."
	case errors.Is(err, workerruntime.ErrProtocol):
		mapped.Code = "EXECUTION_OUTCOME_UNKNOWN"
		mapped.Message = "Worker 응답을 확인할 수 없어 실행 결과가 불명확합니다."
	case errors.Is(err, workerruntime.ErrExecutionFailed):
		mapped.Code = "EXECUTION_FAILED"
		mapped.Message = "검증 명령이 성공하지 못했습니다."
	case errors.Is(err, permissionreview.ErrInvalidResolution), errors.Is(err, executionstore.ErrInvalidCommand):
		mapped.Code = "PERMISSION_REVIEW_INVALID"
		mapped.Message = "권한 검토 요청 또는 선택값을 확인해 주세요."
	case errors.Is(err, executionstore.ErrConflict), errors.Is(err, executionstore.ErrGrantUnavailable):
		mapped.Code = "PERMISSION_REVIEW_CONFLICT"
		mapped.Message = "권한 요청이 이미 처리됐거나 현재 작업과 맞지 않습니다."
	case errors.Is(err, executionstore.ErrIdempotencyConflict):
		mapped.Code = "PERMISSION_REQUEST_CONFLICT"
		mapped.Message = "같은 요청 식별자가 다른 권한 검토에 사용되었습니다."
	case errors.Is(err, executionstore.ErrNotFound):
		mapped.Code = "PERMISSION_REQUEST_NOT_FOUND"
		mapped.Message = "요청한 권한 검토 항목을 찾을 수 없습니다."
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
	case errors.Is(err, vaultbootstrap.ErrNotOpen):
		mapped.Code = "VAULT_NOT_OPEN"
		mapped.Message = "Task Contract를 저장하려면 Vault를 먼저 열어 주세요."
	case errors.Is(err, taskstore.ErrInvalidCommand):
		mapped.Code = "TASK_CONTRACT_INVALID"
		mapped.Message = "Task Contract의 목표, 범위, 완료 조건을 확인해 주세요."
	case errors.Is(err, taskstore.ErrIdempotencyConflict), errors.Is(err, taskstore.ErrIdempotencyUnverified):
		mapped.Code = "TASK_REQUEST_CONFLICT"
		mapped.Message = "같은 요청 식별자가 다른 Task Contract에 사용되었습니다."
	case errors.Is(err, taskstore.ErrRevisionConflict):
		mapped.Code = "TASK_REVISION_CONFLICT"
		mapped.Message = "Task Contract가 다른 요청에서 변경되었습니다. 최신 revision을 다시 확인해 주세요."
	case errors.Is(err, vaultbootstrap.ErrTaskWorkspaceMismatch):
		mapped.Code = "TASK_WORKSPACE_MISMATCH"
		mapped.Message = "이 Task Contract는 현재 Workspace 기준점에 속하지 않습니다."
	case errors.Is(err, taskstore.ErrNotFound):
		mapped.Code = "TASK_NOT_FOUND"
		mapped.Message = "요청한 Task를 찾을 수 없습니다."
	case errors.Is(err, decisionstore.ErrInvalidCommand):
		mapped.Code = "DECISION_INPUT_INVALID"
		mapped.Message = "Decision 질문, 범위, 선택지 또는 답변을 확인해 주세요."
	case errors.Is(err, decisionstore.ErrQuestionStale):
		mapped.Code = "DECISION_REVISION_CONFLICT"
		mapped.Message = "질문이 변경되어 이 답변을 적용할 수 없습니다."
	case errors.Is(err, decisionstore.ErrRepositoryStale):
		mapped.Code = "DECISION_REPOSITORY_CHANGED"
		mapped.Message = "저장소 기준점이 달라 이 답변을 적용할 수 없습니다."
	case errors.Is(err, decisionstore.ErrConflictRequired):
		mapped.Code = "DECISION_CONFLICT_REQUIRED"
		mapped.Message = "충돌 상태인 Decision만 답변을 확정할 수 있습니다."
	case errors.Is(err, decisionstore.ErrAnswerNotFound):
		mapped.Code = "DECISION_ANSWER_NOT_FOUND"
		mapped.Message = "현재 질문 revision에 속한 답변을 찾을 수 없습니다."
	case errors.Is(err, decisionstore.ErrIdempotencyConflict), errors.Is(err, decisionstore.ErrIdempotencyUnverified):
		mapped.Code = "DECISION_REQUEST_CONFLICT"
		mapped.Message = "같은 요청 식별자가 다른 Decision 작업에 사용되었습니다."
	case errors.Is(err, decisionstore.ErrNotFound):
		mapped.Code = "DECISION_NOT_FOUND"
		mapped.Message = "요청한 Decision을 찾을 수 없습니다."
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
	case errors.Is(err, repository.ErrInvalidPath):
		mapped.Code = "WORKSPACE_PATH_INVALID"
		mapped.Message = "저장소 폴더 경로를 확인해 주세요."
	case errors.Is(err, errWorkspaceNotOpen):
		mapped.Code = "WORKSPACE_NOT_OPEN"
		mapped.Message = "Task Contract를 만들 저장소를 먼저 열어 주세요."
	case errors.Is(err, errWorkspaceDirty):
		mapped.Code = "WORKSPACE_DIRTY"
		mapped.Message = "커밋되지 않은 변경이 없는 저장소에서 Task Contract를 만들어 주세요."
	case errors.Is(err, errWorkspaceChanged):
		mapped.Code = "WORKSPACE_BASELINE_CHANGED"
		mapped.Message = "저장소 기준 커밋이 바뀌었습니다. 상태를 다시 확인해 주세요."
	case errors.Is(err, repository.ErrGitUnavailable):
		mapped.Code = "GIT_UNAVAILABLE"
		mapped.Message = "이 기기에서 시스템 Git을 사용할 수 없습니다."
	case errors.Is(err, repository.ErrNotRepository):
		mapped.Code = "WORKSPACE_NOT_GIT_REPOSITORY"
		mapped.Message = "지원되는 Git worktree를 찾을 수 없습니다."
	case errors.Is(err, repository.ErrNoBaselineCommit):
		mapped.Code = "WORKSPACE_BASELINE_MISSING"
		mapped.Message = "첫 커밋이 없는 저장소는 아직 작업 기준점으로 사용할 수 없습니다."
	case errors.Is(err, repository.ErrOutputLimit):
		mapped.Code = "WORKSPACE_CHANGE_LIMIT_EXCEEDED"
		mapped.Message = "변경 파일이 너무 많아 안전하게 저장소 상태를 검사하지 못했습니다."
	case errors.Is(err, repository.ErrInspectionFailed):
		mapped.Code = "WORKSPACE_INSPECTION_FAILED"
		mapped.Message = "Git 저장소 상태를 안전하게 검사하지 못했습니다."
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
