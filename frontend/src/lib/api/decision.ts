import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.DecisionService';
const commitPattern = /^[0-9a-f]{40,64}$/;

export type DecisionOption = { id: string; label: string; consequence: string };
export type DecisionAnswer = { answer_id: string; selected_option_id?: string; text?: string; created_at: string };
export type DecisionItem = {
  decision_id: string; task_id: string; question_revision: number; category: 'blocking' | 'quality' | 'follow_up'; state: 'open' | 'answered' | 'conflicted'; expected_repository_revision: string;
  question: string; reason: string; risk_if_unanswered: string; safe_default: { action: string; continuable_scopes: string[] }; blocking_scopes: string[]; options: DecisionOption[]; answer?: DecisionAnswer; created_at: string; updated_at: string;
};
export type DecisionResult = { decision?: DecisionItem; error?: TalosError };
export type DecisionListResult = { decisions: DecisionItem[]; error?: TalosError };

export async function listDecisions(taskID: string): Promise<DecisionListResult> {
  if (!taskID) throw new Error('DECISION_TASK_INVALID');
  return parseList(await Call.ByName(`${service}.List`, taskID, correlationID()));
}

export async function answerDecision(decisionID: string, questionRevision: number, selectedOptionID: string, text: string): Promise<DecisionResult> {
  if (!decisionID || !Number.isSafeInteger(questionRevision) || questionRevision < 1 || ((selectedOptionID === '') === (text.trim() === ''))) throw new Error('DECISION_ANSWER_INVALID');
  return parseResult(await Call.ByName(`${service}.Answer`, { decision_id: decisionID, question_revision: questionRevision, selected_option_id: selectedOptionID, text: text.trim(), request_id: correlationID(), correlation_id: correlationID() }));
}

function parseList(value: unknown): DecisionListResult {
  if (!isObject(value)) throw new Error('DECISION_RESPONSE_INVALID');
  if (value.error !== undefined) return { decisions: [], error: parseError(value.error) };
  if (!Array.isArray(value.decisions) || value.decisions.length > 256) throw new Error('DECISION_RESPONSE_INVALID');
  return { decisions: value.decisions.map(parseDecision) };
}

function parseResult(value: unknown): DecisionResult {
  if (!isObject(value)) throw new Error('DECISION_RESPONSE_INVALID');
  const result: DecisionResult = {};
  if (value.decision !== undefined) result.decision = parseDecision(value.decision);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.decision === undefined) === (result.error === undefined)) throw new Error('DECISION_RESPONSE_INVALID');
  return result;
}

function parseDecision(value: unknown): DecisionItem {
  if (!isObject(value) || typeof value.decision_id !== 'string' || typeof value.task_id !== 'string' || typeof value.question_revision !== 'number' || !Number.isSafeInteger(value.question_revision) || value.question_revision < 1 || (value.category !== 'blocking' && value.category !== 'quality' && value.category !== 'follow_up') || (value.state !== 'open' && value.state !== 'answered' && value.state !== 'conflicted') || typeof value.expected_repository_revision !== 'string' || !commitPattern.test(value.expected_repository_revision) || typeof value.question !== 'string' || typeof value.reason !== 'string' || typeof value.risk_if_unanswered !== 'string' || !isObject(value.safe_default) || typeof value.safe_default.action !== 'string' || !stringArray(value.safe_default.continuable_scopes, 128) || !stringArray(value.blocking_scopes, 128) || !Array.isArray(value.options) || value.options.length < 2 || value.options.length > 16 || typeof value.created_at !== 'string' || typeof value.updated_at !== 'string' || Number.isNaN(Date.parse(value.created_at)) || Number.isNaN(Date.parse(value.updated_at))) throw new Error('DECISION_RESPONSE_INVALID');
  const options = value.options.map(parseOption);
  const answer = value.answer === undefined ? undefined : parseAnswer(value.answer);
  return { decision_id: value.decision_id, task_id: value.task_id, question_revision: value.question_revision, category: value.category, state: value.state, expected_repository_revision: value.expected_repository_revision, question: value.question, reason: value.reason, risk_if_unanswered: value.risk_if_unanswered, safe_default: { action: value.safe_default.action, continuable_scopes: value.safe_default.continuable_scopes as string[] }, blocking_scopes: value.blocking_scopes as string[], options, answer, created_at: value.created_at, updated_at: value.updated_at };
}

function parseOption(value: unknown): DecisionOption { if (!isObject(value) || typeof value.id !== 'string' || typeof value.label !== 'string' || typeof value.consequence !== 'string') throw new Error('DECISION_RESPONSE_INVALID'); return { id: value.id, label: value.label, consequence: value.consequence }; }
function parseAnswer(value: unknown): DecisionAnswer { if (!isObject(value) || typeof value.answer_id !== 'string' || typeof value.created_at !== 'string' || Number.isNaN(Date.parse(value.created_at)) || (value.selected_option_id !== undefined && typeof value.selected_option_id !== 'string') || (value.text !== undefined && typeof value.text !== 'string')) throw new Error('DECISION_RESPONSE_INVALID'); return { answer_id: value.answer_id, selected_option_id: value.selected_option_id as string | undefined, text: value.text as string | undefined, created_at: value.created_at }; }
function parseError(value: unknown): TalosError { if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('DECISION_RESPONSE_INVALID'); return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id }; }
function stringArray(value: unknown, max: number): value is string[] { return Array.isArray(value) && value.length <= max && value.every((item) => typeof item === 'string'); }
function isObject(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function correlationID(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
