import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.PlanService';

export type ModelProviderStatus = {
  provider_key: string;
  model_key?: string;
  credential_name: string;
  ready: boolean;
  reason_code?: string;
};

export type PlanStep = { id: string; purpose: string; command_index: number };
export type PlanMemory = { memory_id: string; revision: number; statement: string; reason: string };
export type PlanReceipt = {
  receipt_id: string;
  provider_call_id: string;
  context_items: number;
  input_bytes: number;
  output_bytes: number;
  redaction_count: number;
  input_tokens: number;
  cached_input_tokens: number;
  output_tokens: number;
};
export type PlanProposal = {
  state: 'proposed';
  task_id: string;
  contract_revision: number;
  provider_key: string;
  model_key: string;
  summary: string;
  steps: PlanStep[];
  memories: PlanMemory[];
  receipt: PlanReceipt;
};
export type PlanResult = { proposal?: PlanProposal; error?: TalosError };

export async function getModelProviderStatus(): Promise<ModelProviderStatus> {
  return parseStatus(await Call.ByName(`${service}.ProviderStatus`));
}

export async function proposePlan(input: {
  taskID: string;
  providerKey: string;
  modelKey: string;
  workspaceRoot: string;
  baselineCommit: string;
  contractRevision: number;
}): Promise<PlanResult> {
  if (!input.taskID || !input.providerKey || !input.modelKey || !input.workspaceRoot || !/^[0-9a-f]{40}$/.test(input.baselineCommit) || !Number.isSafeInteger(input.contractRevision) || input.contractRevision < 1) {
    throw new Error('PLAN_REQUEST_INVALID');
  }
  return parseResult(await Call.ByName(`${service}.Propose`, {
    task_id: input.taskID,
    request_id: correlationID(),
    correlation_id: correlationID(),
    consent: {
      confirmed: true,
      provider_key: input.providerKey,
      model_key: input.modelKey,
      workspace_root: input.workspaceRoot,
      baseline_commit: input.baselineCommit,
      contract_revision: input.contractRevision,
    },
  }));
}

function parseStatus(value: unknown): ModelProviderStatus {
  if (!isObject(value) || typeof value.provider_key !== 'string' || !value.provider_key || typeof value.credential_name !== 'string' || !value.credential_name || typeof value.ready !== 'boolean') {
    throw new Error('PLAN_STATUS_INVALID');
  }
  if (value.model_key !== undefined && (typeof value.model_key !== 'string' || !value.model_key)) throw new Error('PLAN_STATUS_INVALID');
  if (value.reason_code !== undefined && (typeof value.reason_code !== 'string' || !value.reason_code)) throw new Error('PLAN_STATUS_INVALID');
  if (value.ready && !value.model_key) throw new Error('PLAN_STATUS_INVALID');
  return value as ModelProviderStatus;
}

function parseResult(value: unknown): PlanResult {
  if (!isObject(value)) throw new Error('PLAN_RESPONSE_INVALID');
  const result: PlanResult = {};
  if (value.proposal !== undefined) result.proposal = parseProposal(value.proposal);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.proposal === undefined) === (result.error === undefined)) throw new Error('PLAN_RESPONSE_INVALID');
  return result;
}

function parseProposal(value: unknown): PlanProposal {
  if (!isObject(value) || value.state !== 'proposed' || typeof value.task_id !== 'string' || !value.task_id || !Number.isSafeInteger(value.contract_revision) || (value.contract_revision as number) < 1 || typeof value.provider_key !== 'string' || !value.provider_key || typeof value.model_key !== 'string' || !value.model_key || typeof value.summary !== 'string' || !value.summary || value.summary.length > 2048 || !Array.isArray(value.steps) || value.steps.length < 1 || value.steps.length > 8 || !Array.isArray(value.memories) || value.memories.length > 8) {
    throw new Error('PLAN_RESPONSE_INVALID');
  }
  return {
    state: 'proposed', task_id: value.task_id, contract_revision: value.contract_revision as number,
    provider_key: value.provider_key, model_key: value.model_key, summary: value.summary,
    steps: value.steps.map(parseStep), memories: value.memories.map(parseMemory), receipt: parseReceipt(value.receipt),
  };
}

function parseStep(value: unknown): PlanStep {
  if (!isObject(value) || typeof value.id !== 'string' || !value.id || typeof value.purpose !== 'string' || !value.purpose || !Number.isSafeInteger(value.command_index) || (value.command_index as number) < 0) throw new Error('PLAN_RESPONSE_INVALID');
  return value as PlanStep;
}

function parseMemory(value: unknown): PlanMemory {
  if (!isObject(value) || typeof value.memory_id !== 'string' || !value.memory_id || !Number.isSafeInteger(value.revision) || (value.revision as number) < 1 || typeof value.statement !== 'string' || !value.statement || typeof value.reason !== 'string' || !value.reason) throw new Error('PLAN_RESPONSE_INVALID');
  return value as PlanMemory;
}

function parseReceipt(value: unknown): PlanReceipt {
  if (!isObject(value) || typeof value.receipt_id !== 'string' || !value.receipt_id || typeof value.provider_call_id !== 'string' || !value.provider_call_id) throw new Error('PLAN_RESPONSE_INVALID');
  for (const key of ['context_items', 'input_bytes', 'output_bytes', 'redaction_count', 'input_tokens', 'cached_input_tokens', 'output_tokens'] as const) {
    if (!Number.isSafeInteger(value[key]) || (value[key] as number) < 0) throw new Error('PLAN_RESPONSE_INVALID');
  }
  return value as PlanReceipt;
}

function parseError(value: unknown): TalosError {
  if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('PLAN_RESPONSE_INVALID');
  return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id };
}

function isObject(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function correlationID(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
