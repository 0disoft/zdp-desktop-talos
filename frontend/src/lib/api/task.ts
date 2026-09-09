import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.TaskService';
const commitPattern = /^[0-9a-f]{40,64}$/;

export type TaskContractInput = {
  goal: string;
  allowed_paths: string[];
  forbidden_actions: string[];
  acceptance_criteria: string[];
  verification_commands: VerificationCommandInput[];
  risk: 'low' | 'medium' | 'high';
};

export type VerificationCommandInput = {
  rule_id: string;
  arguments: string[];
  working_directory: string;
};

export type TaskStatus = {
  task_id: string;
  revision: number;
  baseline_commit: string;
  risk: 'low' | 'medium' | 'high';
  status: 'contracted' | 'completed' | 'discarded';
  created_at: string;
};

export type TaskResult = { task?: TaskStatus; error?: TalosError };

export type TaskDetail = Omit<TaskContractInput, 'risk'> & { task: TaskStatus };

export async function listTaskContracts(): Promise<{tasks: TaskDetail[]; error?: TalosError}> {
  const value: unknown = await Call.ByName(`${service}.ListContracts`, correlationID());
  if (!isObject(value) || !Array.isArray(value.tasks) || value.tasks.length > 50) throw new Error('TASK_RESPONSE_INVALID');
  if (value.error !== undefined) return { tasks: [], error: parseError(value.error) };
  const stringList = (input: unknown): string[] => {
    if (!Array.isArray(input) || !input.every((item) => typeof item === 'string')) throw new Error('TASK_RESPONSE_INVALID');
    return input;
  };
  return { tasks: value.tasks.map((item): TaskDetail => {
    if (!isObject(item) || typeof item.goal !== 'string' || !Array.isArray(item.verification_commands) || item.verification_commands.length === 0) throw new Error('TASK_RESPONSE_INVALID');
    return { task: parseTask(item.task), goal: item.goal, allowed_paths: stringList(item.allowed_paths), forbidden_actions: stringList(item.forbidden_actions), acceptance_criteria: stringList(item.acceptance_criteria), verification_commands: item.verification_commands.map((command): VerificationCommandInput => {
      if (!isObject(command) || typeof command.rule_id !== 'string' || typeof command.working_directory !== 'string') throw new Error('TASK_RESPONSE_INVALID');
      return { rule_id: command.rule_id, arguments: stringList(command.arguments), working_directory: command.working_directory };
    }) };
  }) };
}

export async function createTaskContract(input: TaskContractInput): Promise<TaskResult> {
  const request = { ...input, request_id: correlationID(), correlation_id: correlationID() };
  return parseResult(await Call.ByName(`${service}.CreateContract`, request));
}

export async function reviseTaskContract(taskID: string, expectedRevision: number, input: TaskContractInput): Promise<TaskResult> {
  if (!taskID || !Number.isSafeInteger(expectedRevision) || expectedRevision < 1) throw new Error('TASK_REVISION_INVALID');
  const request = { ...input, task_id: taskID, expected_revision: expectedRevision, request_id: correlationID(), correlation_id: correlationID() };
  return parseResult(await Call.ByName(`${service}.ReviseContract`, request));
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
  if (!isObject(value) || typeof value.task_id !== 'string' || value.task_id.length === 0 || typeof value.revision !== 'number' || !Number.isSafeInteger(value.revision) || value.revision < 1 || typeof value.baseline_commit !== 'string' || !commitPattern.test(value.baseline_commit) || (value.risk !== 'low' && value.risk !== 'medium' && value.risk !== 'high') || (value.status !== 'contracted' && value.status !== 'completed' && value.status !== 'discarded') || typeof value.created_at !== 'string' || Number.isNaN(new Date(value.created_at).getTime())) {
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
