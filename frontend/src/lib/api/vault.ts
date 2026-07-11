import { Call } from '@wailsio/runtime';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.VaultService';

export type VaultStatus = {
  state: 'locked' | 'unlocked';
  persistent_key_store: boolean;
  vault_id?: string;
  revision?: number;
  retention_days?: number;
};

export type TalosError = {
  code: string;
  message: string;
  retryable: boolean;
  correlation_id: string;
};

export type VaultResult = {
  vault?: VaultStatus;
  error?: TalosError;
};

export async function getVaultStatus(): Promise<VaultStatus> {
  return parseStatus(await Call.ByName(`${service}.Status`));
}

export async function createVault(retentionDays: number): Promise<VaultResult> {
  return parseResult(await Call.ByName(`${service}.Create`, retentionDays, correlationID()));
}

export async function lockVault(): Promise<VaultResult> {
  return parseResult(await Call.ByName(`${service}.Lock`, correlationID()));
}

function parseResult(value: unknown): VaultResult {
  if (!isObject(value)) {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  const result: VaultResult = {};
  if (value.vault !== undefined) {
    result.vault = parseStatus(value.vault);
  }
  if (value.error !== undefined) {
    result.error = parseError(value.error);
  }
  if ((result.vault === undefined) === (result.error === undefined)) {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  return result;
}

function parseStatus(value: unknown): VaultStatus {
  if (
    !isObject(value) ||
    (value.state !== 'locked' && value.state !== 'unlocked') ||
    typeof value.persistent_key_store !== 'boolean'
  ) {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  const status: VaultStatus = {
    state: value.state,
    persistent_key_store: value.persistent_key_store,
  };
  if (typeof value.vault_id === 'string') status.vault_id = value.vault_id;
  if (typeof value.revision === 'number' && Number.isSafeInteger(value.revision)) status.revision = value.revision;
  if (typeof value.retention_days === 'number' && Number.isSafeInteger(value.retention_days)) {
    status.retention_days = value.retention_days;
  }
  if (status.state === 'unlocked' && (!status.vault_id || !status.revision || !status.retention_days)) {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  return status;
}

function parseError(value: unknown): TalosError {
  if (
    !isObject(value) ||
    typeof value.code !== 'string' ||
    typeof value.message !== 'string' ||
    typeof value.retryable !== 'boolean' ||
    typeof value.correlation_id !== 'string'
  ) {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  return {
    code: value.code,
    message: value.message,
    retryable: value.retryable,
    correlation_id: value.correlation_id,
  };
}

function isObject(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null && !Array.isArray(value);
}

function correlationID(): string {
  return typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
