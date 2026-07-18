import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.MemoryService';

export type MemoryState = 'candidate' | 'approved' | 'stable' | 'stale' | 'rejected' | 'quarantined' | 'superseded' | 'deprecated';
export type MemoryReviewOutcome = 'approved' | 'rejected' | 'quarantined';
export type MemoryItem = {
  memory_id: string;
  kind: 'preference' | 'constraint' | 'decision' | 'procedure' | 'failure_pattern' | 'environment_fact';
  state: MemoryState;
  scope: 'vault' | 'workspace';
  statement: string;
  rationale: string;
  goal_terms: string[];
  evidence_event_ids: string[];
  source_actor: string;
  confidence: number;
  sensitivity: 'public' | 'private' | 'sensitive';
  revision: number;
  created_at: string;
  updated_at: string;
};
export type AppliedMemory = { memory_id: string; revision: number; statement: string; reason: string; source_ref: string; sensitivity: 'public' | 'private' | 'sensitive' };
export type MemoryListResult = { memories: MemoryItem[]; error?: TalosError };
export type MemoryCompileResult = { eligible: number; memories: MemoryItem[]; error?: TalosError };
export type MemoryResult = { memory?: MemoryItem; error?: TalosError };
export type MemoryContextResult = { considered: number; selected_bytes: number; items: AppliedMemory[]; error?: TalosError };

export async function listMemoryCandidates(): Promise<MemoryListResult> {
  return parseList(await Call.ByName(`${service}.ListCandidates`, correlationID()));
}

export async function compileTaskMemories(taskID: string): Promise<MemoryCompileResult> {
  if (!taskID) throw new Error('MEMORY_TASK_INVALID');
  return parseCompile(await Call.ByName(`${service}.CompileTask`, { task_id: taskID, request_id: correlationID(), correlation_id: correlationID() }));
}

export async function reviewMemory(memoryID: string, expectedRevision: number, outcome: MemoryReviewOutcome, reason: string): Promise<MemoryResult> {
  if (!memoryID || !Number.isSafeInteger(expectedRevision) || expectedRevision < 1 || !reason.trim()) throw new Error('MEMORY_REVIEW_INVALID');
  return parseResult(await Call.ByName(`${service}.Review`, { memory_id: memoryID, expected_revision: expectedRevision, outcome, reason: reason.trim(), request_id: correlationID(), correlation_id: correlationID() }));
}

export async function explainTaskMemory(taskID: string): Promise<MemoryContextResult> {
  if (!taskID) throw new Error('MEMORY_TASK_INVALID');
  return parseContext(await Call.ByName(`${service}.ExplainCurrentTask`, taskID, correlationID()));
}

function parseList(value: unknown): MemoryListResult {
  if (!isObject(value)) throw new Error('MEMORY_RESPONSE_INVALID');
  if (value.error !== undefined) return { memories: [], error: parseError(value.error) };
  return { memories: parseMemories(value.memories) };
}

function parseCompile(value: unknown): MemoryCompileResult {
  if (!isObject(value)) throw new Error('MEMORY_RESPONSE_INVALID');
  if (value.error !== undefined) return { eligible: 0, memories: [], error: parseError(value.error) };
  if (!Number.isSafeInteger(value.eligible) || (value.eligible as number) < 0 || (value.eligible as number) > 256) throw new Error('MEMORY_RESPONSE_INVALID');
  return { eligible: value.eligible as number, memories: parseMemories(value.memories) };
}

function parseResult(value: unknown): MemoryResult {
  if (!isObject(value)) throw new Error('MEMORY_RESPONSE_INVALID');
  const result: MemoryResult = {};
  if (value.memory !== undefined) result.memory = parseMemory(value.memory);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.memory === undefined) === (result.error === undefined)) throw new Error('MEMORY_RESPONSE_INVALID');
  return result;
}

function parseContext(value: unknown): MemoryContextResult {
  if (!isObject(value)) throw new Error('MEMORY_RESPONSE_INVALID');
  if (value.error !== undefined) return { considered: 0, selected_bytes: 0, items: [], error: parseError(value.error) };
  if (!Number.isSafeInteger(value.considered) || (value.considered as number) < 0 || (value.considered as number) > 200 || !Number.isSafeInteger(value.selected_bytes) || (value.selected_bytes as number) < 0 || (value.selected_bytes as number) > 65536 || !Array.isArray(value.items) || value.items.length > 32) throw new Error('MEMORY_RESPONSE_INVALID');
  return { considered: value.considered as number, selected_bytes: value.selected_bytes as number, items: value.items.map(parseAppliedMemory) };
}

function parseMemories(value: unknown): MemoryItem[] {
  if (!Array.isArray(value) || value.length > 200) throw new Error('MEMORY_RESPONSE_INVALID');
  return value.map(parseMemory);
}

function parseMemory(value: unknown): MemoryItem {
  if (!isObject(value) || typeof value.memory_id !== 'string' || !value.memory_id || !memoryKind(value.kind) || !memoryState(value.state) || (value.scope !== 'vault' && value.scope !== 'workspace') || typeof value.statement !== 'string' || !value.statement || value.statement.length > 4096 || typeof value.rationale !== 'string' || !value.rationale || value.rationale.length > 2048 || !stringArray(value.goal_terms, 16) || !stringArray(value.evidence_event_ids, 32) || value.evidence_event_ids.length < 1 || typeof value.source_actor !== 'string' || !value.source_actor || value.source_actor.length > 64 || !Number.isSafeInteger(value.confidence) || (value.confidence as number) < 0 || (value.confidence as number) > 100 || (value.sensitivity !== 'public' && value.sensitivity !== 'private' && value.sensitivity !== 'sensitive') || !Number.isSafeInteger(value.revision) || (value.revision as number) < 1 || typeof value.created_at !== 'string' || Number.isNaN(Date.parse(value.created_at)) || typeof value.updated_at !== 'string' || Number.isNaN(Date.parse(value.updated_at))) throw new Error('MEMORY_RESPONSE_INVALID');
  return value as MemoryItem;
}

function parseAppliedMemory(value: unknown): AppliedMemory {
  if (!isObject(value) || typeof value.memory_id !== 'string' || !value.memory_id || !Number.isSafeInteger(value.revision) || (value.revision as number) < 1 || typeof value.statement !== 'string' || !value.statement || value.statement.length > 4096 || typeof value.reason !== 'string' || !value.reason || typeof value.source_ref !== 'string' || !value.source_ref || (value.sensitivity !== 'public' && value.sensitivity !== 'private' && value.sensitivity !== 'sensitive')) throw new Error('MEMORY_RESPONSE_INVALID');
  return value as AppliedMemory;
}

function memoryKind(value: unknown): value is MemoryItem['kind'] { return value === 'preference' || value === 'constraint' || value === 'decision' || value === 'procedure' || value === 'failure_pattern' || value === 'environment_fact'; }
function memoryState(value: unknown): value is MemoryState { return value === 'candidate' || value === 'approved' || value === 'stable' || value === 'stale' || value === 'rejected' || value === 'quarantined' || value === 'superseded' || value === 'deprecated'; }
function stringArray(value: unknown, max: number): value is string[] { return Array.isArray(value) && value.length <= max && value.every((item) => typeof item === 'string'); }
function parseError(value: unknown): TalosError { if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('MEMORY_RESPONSE_INVALID'); return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id }; }
function isObject(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function correlationID(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
