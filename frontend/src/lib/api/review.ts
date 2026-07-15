import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.PatchReviewService';

export type PatchChange = { path: string; original_path?: string; kind: 'tracked' | 'renamed' | 'untracked'; index_status: string; worktree_status: string };
export type PatchEvidence = { id: string; contract_revision: number; command_index: number; state_hash: string; finished_at: string };
export type PatchDiff = { path: string; binary: boolean; truncated: boolean; omitted_reason?: 'binary' | 'symlink' | 'unsupported_type' | 'file_limit'; text?: string; added_lines: number; deleted_lines: number; findings: number };
export type PatchReview = { task_id: string; contract_revision: number; baseline_commit: string; status: 'fresh' | 'stale' | 'unverified'; reason: 'fresh' | 'no_evidence' | 'baseline_changed' | 'contract_changed' | 'patch_changed'; state_hash: string; patch_hash: string; changes: PatchChange[]; diffs: PatchDiff[]; secret_findings: number; evidence?: PatchEvidence };
export type PatchReviewResult = { review?: PatchReview; error?: TalosError };

export async function getTaskReview(taskID: string): Promise<PatchReviewResult> {
  if (!taskID) throw new Error('PATCH_REVIEW_REQUEST_INVALID');
  return parseResult(await Call.ByName(`${service}.GetTaskReview`, { task_id: taskID, correlation_id: correlationID() }));
}

function parseResult(value: unknown): PatchReviewResult {
  if (!isObject(value)) throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  const result: PatchReviewResult = {};
  if (value.review !== undefined) result.review = parseReview(value.review);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.review === undefined) === (result.error === undefined)) throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  return result;
}

function parseReview(value: unknown): PatchReview {
  if (!isObject(value) || typeof value.task_id !== 'string' || !positiveInteger(value.contract_revision) || !commit(value.baseline_commit) || !hash(value.state_hash) || !hash(value.patch_hash) || !Array.isArray(value.changes) || !Array.isArray(value.diffs) || !nonnegativeInteger(value.secret_findings)) throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  if (value.status !== 'fresh' && value.status !== 'stale' && value.status !== 'unverified') throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  if (!['fresh', 'no_evidence', 'baseline_changed', 'contract_changed', 'patch_changed'].includes(String(value.reason))) throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  const changes = value.changes.map(parseChange);
  const diffs = value.diffs.map(parseDiff);
  const evidence = value.evidence === undefined ? undefined : parseEvidence(value.evidence);
  if (value.status === 'unverified' ? evidence !== undefined : evidence === undefined) throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  return { task_id: value.task_id, contract_revision: value.contract_revision, baseline_commit: value.baseline_commit, status: value.status, reason: value.reason as PatchReview['reason'], state_hash: value.state_hash, patch_hash: value.patch_hash, changes, diffs, secret_findings: value.secret_findings, evidence };
}

function parseDiff(value: unknown): PatchDiff {
  if (!isObject(value) || typeof value.path !== 'string' || !value.path || typeof value.binary !== 'boolean' || typeof value.truncated !== 'boolean' || !nonnegativeInteger(value.added_lines) || !nonnegativeInteger(value.deleted_lines) || !nonnegativeInteger(value.findings)) throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  if (value.text !== undefined && typeof value.text !== 'string') throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  if (value.omitted_reason !== undefined && value.omitted_reason !== 'binary' && value.omitted_reason !== 'symlink' && value.omitted_reason !== 'unsupported_type' && value.omitted_reason !== 'file_limit') throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  if (value.binary && value.text) throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  return { path: value.path, binary: value.binary, truncated: value.truncated, omitted_reason: value.omitted_reason, text: value.text, added_lines: value.added_lines, deleted_lines: value.deleted_lines, findings: value.findings };
}

function parseChange(value: unknown): PatchChange {
  if (!isObject(value) || typeof value.path !== 'string' || !value.path || (value.kind !== 'tracked' && value.kind !== 'renamed' && value.kind !== 'untracked') || typeof value.index_status !== 'string' || value.index_status.length !== 1 || typeof value.worktree_status !== 'string' || value.worktree_status.length !== 1) throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  if (value.original_path !== undefined && typeof value.original_path !== 'string') throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  return { path: value.path, original_path: value.original_path, kind: value.kind, index_status: value.index_status, worktree_status: value.worktree_status };
}

function parseEvidence(value: unknown): PatchEvidence {
  if (!isObject(value) || typeof value.id !== 'string' || !positiveInteger(value.contract_revision) || !Number.isSafeInteger(value.command_index) || Number(value.command_index) < 0 || !hash(value.state_hash) || typeof value.finished_at !== 'string' || Number.isNaN(Date.parse(value.finished_at))) throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  return { id: value.id, contract_revision: value.contract_revision, command_index: value.command_index as number, state_hash: value.state_hash, finished_at: value.finished_at };
}

function parseError(value: unknown): TalosError {
  if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('PATCH_REVIEW_RESPONSE_INVALID');
  return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id };
}
function isObject(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function positiveInteger(value: unknown): value is number { return typeof value === 'number' && Number.isSafeInteger(value) && value > 0; }
function nonnegativeInteger(value: unknown): value is number { return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0; }
function hash(value: unknown): value is string { return typeof value === 'string' && /^[0-9a-f]{64}$/.test(value); }
function commit(value: unknown): value is string { return typeof value === 'string' && /^[0-9a-f]{40,64}$/.test(value); }
function correlationID(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
