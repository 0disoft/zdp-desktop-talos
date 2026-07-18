import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.AccountService';

export type AccountStatus = {
  mode: 'local_only';
  state: 'vault_locked' | 'unlinked' | 'linked';
  link_available: boolean;
  reason_code: 'VAULT_NOT_OPEN' | 'PRODUCT_LINK_UPSTREAM_NOT_READY';
  revision?: number;
};

export type AccountStatusResult = { account?: AccountStatus; error?: TalosError };

export async function getAccountStatus(): Promise<AccountStatusResult> {
  return parseResult(await Call.ByName(`${service}.Status`, correlationID()));
}

export async function unlinkAccount(expectedRevision: number): Promise<AccountStatusResult> {
  const requestID = correlationID();
  return parseResult(await Call.ByName(`${service}.Unlink`, { expected_revision: expectedRevision, request_id: requestID, correlation_id: requestID }));
}

function parseResult(value: unknown): AccountStatusResult {
  if (!isObject(value)) throw new Error('ACCOUNT_RESPONSE_INVALID');
  if (value.error !== undefined) return { error: parseError(value.error) };
  if (!isObject(value.account)) throw new Error('ACCOUNT_RESPONSE_INVALID');
  const account = value.account;
  if (account.mode !== 'local_only' || !['vault_locked', 'unlinked', 'linked'].includes(String(account.state)) || typeof account.link_available !== 'boolean' || !['VAULT_NOT_OPEN', 'PRODUCT_LINK_UPSTREAM_NOT_READY'].includes(String(account.reason_code)) || account.link_available || (account.revision !== undefined && (!Number.isSafeInteger(account.revision) || (account.revision as number) < 1))) throw new Error('ACCOUNT_RESPONSE_INVALID');
  return { account: account as AccountStatus };
}

function parseError(value: unknown): TalosError {
  if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('ACCOUNT_RESPONSE_INVALID');
  return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id };
}

function isObject(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function correlationID(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
