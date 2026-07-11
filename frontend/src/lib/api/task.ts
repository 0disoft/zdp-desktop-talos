import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.TaskService';
const commitPattern = /^[0-9a-f]{40,64}$/;

export type TaskContractInput = {
  goal: string;
  allowed_paths: string[];
  forbidden_actions: string[];
  acceptance_criteria: string[];
  risk: 'low' | 'medium' | 'high';
};

export type TaskStatus = {
  task_id: string;
  revision: number;
  baseline_commit: string;
  risk: 'low' | 'medium' | 'high';
  status: 'contracted';
  created_at: string;
};

export type TaskResult = { task?: TaskStatus; error?: TalosError };

export async function createTaskContract(input: TaskContractInput): Promise<TaskResult> {
  const request = { ...input, request_id: correlationID(), correlation_id: correlationID() };
  return parseResult(await Call.ByName(`${service}.CreateContract`, request));
}

function parseResult(value: unknown): TaskResult {
  if (!isObject(value)) throw new Error('TASK_RESPONSE_INVALID');
  const result: TaskResult = {};
  if (value.task !== undefined) result.task = parseTask(value.task);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.task === undefined) === (result.error === undefined)) throw new Error('TASK_RESPONSE_INVALID');
  return result;
}

function parseTask(value: unknown): TaskStatus {
  if (!isObject(value) || typeof value.task_id !== 'string' || value.task_id.length === 0 || value.revision !== 1 || typeof value.baseline_commit !== 'string' || !commitPattern.test(value.baseline_commit) || (value.risk !== 'low' && value.risk !== 'medium' && value.risk !== 'high') || value.status !== 'contracted' || typeof value.created_at !== 'string' || Number.isNaN(new Date(value.created_at).getTime())) {
    throw new Error('TASK_RESPONSE_INVALID');
  }
  return { task_id: value.task_id, revision: value.revision, baseline_commit: value.baseline_commit, risk: value.risk, status: value.status, created_at: value.created_at };
}

function parseError(value: unknown): TalosError {
  if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('TASK_RESPONSE_INVALID');
  return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id };
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function correlationID(): string {
  return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
