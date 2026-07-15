import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.PatchService';

export type PatchCommand = {
  action_id: string;
  kind: 'apply' | 'discard';
  state: 'pending' | 'succeeded' | 'failed' | 'unknown';
  task_status: 'contracted' | 'completed' | 'discarded';
  safe_error_code?: string;
  replayed: boolean;
};

export type PatchCommandResult = { command?: PatchCommand; error?: TalosError };

export async function applyPatch(taskID: string, expectedRevision: number, expectedPatchHash: string): Promise<PatchCommandResult> {
  return execute('ApplyPatch', taskID, expectedRevision, expectedPatchHash);
}

export async function discardPatch(taskID: string, expectedRevision: number, expectedPatchHash: string): Promise<PatchCommandResult> {
  return execute('DiscardPatch', taskID, expectedRevision, expectedPatchHash);
}

async function execute(method: 'ApplyPatch' | 'DiscardPatch', taskID: string, expectedRevision: number, expectedPatchHash: string): Promise<PatchCommandResult> {
  if (!taskID || !Number.isSafeInteger(expectedRevision) || expectedRevision < 1 || !/^[0-9a-f]{64}$/.test(expectedPatchHash)) throw new Error('PATCH_COMMAND_REQUEST_INVALID');
  const requestID = correlationID();
  return parseResult(await Call.ByName(`${service}.${method}`, { task_id: taskID, expected_revision: expectedRevision, expected_patch_hash: expectedPatchHash, request_id: requestID, correlation_id: requestID }));
}

function parseResult(value: unknown): PatchCommandResult {
  if (!isObject(value)) throw new Error('PATCH_COMMAND_RESPONSE_INVALID');
  const result: PatchCommandResult = {};
  if (value.command !== undefined) result.command = parseCommand(value.command);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.command === undefined) === (result.error === undefined)) throw new Error('PATCH_COMMAND_RESPONSE_INVALID');
  return result;
}

function parseCommand(value: unknown): PatchCommand {
  if (!isObject(value) || typeof value.action_id !== 'string' || !value.action_id || (value.kind !== 'apply' && value.kind !== 'discard') || !['pending', 'succeeded', 'failed', 'unknown'].includes(String(value.state)) || !['contracted', 'completed', 'discarded'].includes(String(value.task_status)) || typeof value.replayed !== 'boolean') throw new Error('PATCH_COMMAND_RESPONSE_INVALID');
  if (value.safe_error_code !== undefined && typeof value.safe_error_code !== 'string') throw new Error('PATCH_COMMAND_RESPONSE_INVALID');
  return { action_id: value.action_id, kind: value.kind, state: value.state as PatchCommand['state'], task_status: value.task_status as PatchCommand['task_status'], safe_error_code: value.safe_error_code, replayed: value.replayed };
}

function parseError(value: unknown): TalosError {
  if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('PATCH_COMMAND_RESPONSE_INVALID');
  return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id };
}

function isObject(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function correlationID(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
