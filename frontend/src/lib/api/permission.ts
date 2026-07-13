import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.PermissionService';

export type PermissionOutcome = 'deny' | 'allow_once' | 'allow_task';
export type PermissionRequest = {
  request_id: string; task_id: string; rule_id: string; executable: string; arguments: string[];
  environment_names: string[]; timeout_ms: number; max_output_bytes: number; created_at: string;
};
export type PermissionListResult = { requests: PermissionRequest[]; error?: TalosError };
export type PermissionResult = { resolution?: { request_id: string; state: 'approved' | 'denied'; outcome: PermissionOutcome; expires_at: string }; error?: TalosError };

export async function listPermissionRequests(taskID: string): Promise<PermissionListResult> {
  if (!taskID) throw new Error('PERMISSION_TASK_INVALID');
  return parseList(await Call.ByName(`${service}.List`, taskID, correlationID()));
}

export async function resolvePermissionRequest(requestID: string, outcome: PermissionOutcome): Promise<PermissionResult> {
  if (!requestID || !['deny', 'allow_once', 'allow_task'].includes(outcome)) throw new Error('PERMISSION_RESOLUTION_INVALID');
  return parseResult(await Call.ByName(`${service}.Resolve`, { request_id: requestID, outcome, request_token: correlationID(), correlation_id: correlationID() }));
}

function parseList(value: unknown): PermissionListResult {
  if (!isObject(value)) throw new Error('PERMISSION_RESPONSE_INVALID');
  if (value.error !== undefined) return { requests: [], error: parseError(value.error) };
  if (!Array.isArray(value.requests) || value.requests.length > 256) throw new Error('PERMISSION_RESPONSE_INVALID');
  return { requests: value.requests.map(parseRequest) };
}

function parseResult(value: unknown): PermissionResult {
  if (!isObject(value)) throw new Error('PERMISSION_RESPONSE_INVALID');
  const result: PermissionResult = {};
  if (value.error !== undefined) result.error = parseError(value.error);
  if (value.resolution !== undefined) {
    const item = value.resolution;
    if (!isObject(item) || typeof item.request_id !== 'string' || (item.state !== 'approved' && item.state !== 'denied') || (item.outcome !== 'deny' && item.outcome !== 'allow_once' && item.outcome !== 'allow_task') || typeof item.expires_at !== 'string' || Number.isNaN(Date.parse(item.expires_at))) throw new Error('PERMISSION_RESPONSE_INVALID');
    result.resolution = { request_id: item.request_id, state: item.state, outcome: item.outcome, expires_at: item.expires_at };
  }
  if ((result.error === undefined) === (result.resolution === undefined)) throw new Error('PERMISSION_RESPONSE_INVALID');
  return result;
}

function parseRequest(value: unknown): PermissionRequest {
  if (!isObject(value) || typeof value.request_id !== 'string' || typeof value.task_id !== 'string' || typeof value.rule_id !== 'string' || typeof value.executable !== 'string' || !stringArray(value.arguments, 256) || !stringArray(value.environment_names, 32) || typeof value.timeout_ms !== 'number' || !Number.isSafeInteger(value.timeout_ms) || value.timeout_ms < 1 || typeof value.max_output_bytes !== 'number' || !Number.isSafeInteger(value.max_output_bytes) || value.max_output_bytes < 1 || typeof value.created_at !== 'string' || Number.isNaN(Date.parse(value.created_at))) throw new Error('PERMISSION_RESPONSE_INVALID');
  return { request_id: value.request_id, task_id: value.task_id, rule_id: value.rule_id, executable: value.executable, arguments: value.arguments as string[], environment_names: value.environment_names as string[], timeout_ms: value.timeout_ms, max_output_bytes: value.max_output_bytes, created_at: value.created_at };
}

function parseError(value: unknown): TalosError { if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('PERMISSION_RESPONSE_INVALID'); return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id }; }
function stringArray(value: unknown, max: number): value is string[] { return Array.isArray(value) && value.length <= max && value.every((item) => typeof item === 'string'); }
function isObject(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function correlationID(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
