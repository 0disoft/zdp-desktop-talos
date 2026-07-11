import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.WorkspaceService';
const commitPattern = /^[0-9a-f]{40,64}$/;

export type WorkspaceStatus = {
  state: 'closed' | 'open';
  root?: string;
  baseline_commit?: string;
  head_ref?: string;
  detached?: boolean;
  dirty?: boolean;
  change_count?: number;
  captured_at?: string;
};

export type WorkspaceResult = {
  workspace?: WorkspaceStatus;
  error?: TalosError;
};

export async function inspectRepository(path: string): Promise<WorkspaceResult> {
  if (!path || path.length > 32767 || path.includes('\0')) throw new Error('WORKSPACE_PATH_INVALID');
  return parseResult(await Call.ByName(`${service}.InspectRepository`, path, correlationID()));
}

export async function closeWorkspace(): Promise<WorkspaceResult> {
  return parseResult(await Call.ByName(`${service}.Close`));
}

function parseResult(value: unknown): WorkspaceResult {
  if (!isObject(value)) throw new Error('WORKSPACE_RESPONSE_INVALID');
  const result: WorkspaceResult = {};
  if (value.workspace !== undefined) result.workspace = parseStatus(value.workspace);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.workspace === undefined) === (result.error === undefined)) throw new Error('WORKSPACE_RESPONSE_INVALID');
  return result;
}

function parseStatus(value: unknown): WorkspaceStatus {
  if (!isObject(value) || (value.state !== 'closed' && value.state !== 'open')) throw new Error('WORKSPACE_RESPONSE_INVALID');
  if (value.state === 'closed') return { state: 'closed' };
  if (
    typeof value.root !== 'string' ||
    typeof value.baseline_commit !== 'string' ||
    !commitPattern.test(value.baseline_commit) ||
    typeof value.detached !== 'boolean' ||
    typeof value.dirty !== 'boolean' ||
    typeof value.change_count !== 'number' ||
    !Number.isSafeInteger(value.change_count) ||
    value.change_count < 0 ||
    value.change_count > 4096 ||
    typeof value.captured_at !== 'string' ||
    Number.isNaN(new Date(value.captured_at).getTime())
  ) {
    throw new Error('WORKSPACE_RESPONSE_INVALID');
  }
  if (value.detached ? value.head_ref !== undefined && value.head_ref !== '' : typeof value.head_ref !== 'string' || !value.head_ref) {
    throw new Error('WORKSPACE_RESPONSE_INVALID');
  }
  if (value.dirty !== (value.change_count > 0)) throw new Error('WORKSPACE_RESPONSE_INVALID');
  return {
    state: 'open', root: value.root, baseline_commit: value.baseline_commit,
    head_ref: value.detached ? undefined : value.head_ref as string,
    detached: value.detached, dirty: value.dirty, change_count: value.change_count,
    captured_at: value.captured_at,
  };
}

function parseError(value: unknown): TalosError {
  if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') {
    throw new Error('WORKSPACE_RESPONSE_INVALID');
  }
  return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id };
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function correlationID(): string {
  return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
