import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.ExecutionService';

export type ExecutionStatus =
  | {
      state: 'review_required';
      outcome: 'require_review';
      permission_request_id: string;
      replayed: boolean;
    }
  | {
      state: 'succeeded';
      outcome: 'allow_once' | 'allow_task' | 'allow_workspace';
      run_id: string;
      attempt_id: string;
      exit_code: number;
      replayed: boolean;
      evidence_id: string;
      contract_revision: number;
      command_index: number;
      worktree_state_hash: string;
    };

export type ExecutionResult = { execution?: ExecutionStatus; error?: TalosError };

export async function executeVerification(taskID: string, commandIndex = 0): Promise<ExecutionResult> {
  if (!taskID || !Number.isSafeInteger(commandIndex) || commandIndex < 0) throw new Error('EXECUTION_REQUEST_INVALID');
  const request = { task_id: taskID, command_index: commandIndex, request_id: correlationID(), correlation_id: correlationID() };
  return parseResult(await Call.ByName(`${service}.ExecuteVerification`, request));
}

function parseResult(value: unknown): ExecutionResult {
  if (!isObject(value)) throw new Error('EXECUTION_RESPONSE_INVALID');
  const result: ExecutionResult = {};
  if (value.execution !== undefined) result.execution = parseExecution(value.execution);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.execution === undefined) === (result.error === undefined)) throw new Error('EXECUTION_RESPONSE_INVALID');
  return result;
}

function parseExecution(value: unknown): ExecutionStatus {
  if (!isObject(value) || typeof value.state !== 'string' || typeof value.outcome !== 'string' || typeof value.replayed !== 'boolean') {
    throw new Error('EXECUTION_RESPONSE_INVALID');
  }
  if (value.state === 'review_required' && value.outcome === 'require_review' && typeof value.permission_request_id === 'string' && value.permission_request_id.length > 0) {
    return { state: value.state, outcome: value.outcome, permission_request_id: value.permission_request_id, replayed: value.replayed };
  }
  if (value.state === 'succeeded' && (value.outcome === 'allow_once' || value.outcome === 'allow_task' || value.outcome === 'allow_workspace') && typeof value.run_id === 'string' && value.run_id.length > 0 && typeof value.attempt_id === 'string' && value.attempt_id.length > 0 && typeof value.exit_code === 'number' && Number.isSafeInteger(value.exit_code) && typeof value.evidence_id === 'string' && value.evidence_id.length > 0 && typeof value.contract_revision === 'number' && Number.isSafeInteger(value.contract_revision) && value.contract_revision > 0 && typeof value.command_index === 'number' && Number.isSafeInteger(value.command_index) && value.command_index >= 0 && typeof value.worktree_state_hash === 'string' && /^[0-9a-f]{64}$/.test(value.worktree_state_hash)) {
    return { state: value.state, outcome: value.outcome, run_id: value.run_id, attempt_id: value.attempt_id, exit_code: value.exit_code, replayed: value.replayed, evidence_id: value.evidence_id, contract_revision: value.contract_revision, command_index: value.command_index, worktree_state_hash: value.worktree_state_hash };
  }
  throw new Error('EXECUTION_RESPONSE_INVALID');
}

function parseError(value: unknown): TalosError {
  if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('EXECUTION_RESPONSE_INVALID');
  return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id };
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function correlationID(): string {
  return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
