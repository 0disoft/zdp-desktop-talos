import { Call } from '@wailsio/runtime';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.VaultService';
const uuidV7 = /^[0-9a-f]{8}-[0-9a-f]{4}-7[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$/;

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

export type VaultSummary = {
  vault_id: string;
  created_at: string;
};

export type VaultListResult = {
  vaults: VaultSummary[];
  error?: TalosError;
};

export async function getVaultStatus(): Promise<VaultStatus> {
  return parseStatus(await Call.ByName(`${service}.Status`));
}

export async function createVault(retentionDays: number): Promise<VaultResult> {
  return parseResult(await Call.ByName(`${service}.Create`, retentionDays, correlationID()));
}

export async function listVaults(): Promise<VaultListResult> {
  return parseListResult(await Call.ByName(`${service}.List`, correlationID()));
}

export async function openVault(vaultID: string): Promise<VaultResult> {
  if (!uuidV7.test(vaultID)) {
    throw new Error('VAULT_ID_INVALID');
  }
  return parseResult(await Call.ByName(`${service}.Open`, vaultID, correlationID()));
}

export async function lockVault(): Promise<VaultResult> {
  return parseResult(await Call.ByName(`${service}.Lock`, correlationID()));
}

export async function updateVaultRetention(retentionDays: number, expectedRevision: number): Promise<VaultResult> {
  const requestID = correlationID();
  return parseResult(
    await Call.ByName(`${service}.UpdateRetention`, retentionDays, expectedRevision, requestID, correlationID()),
  );
}

export async function hardPurgeVault(expectedRevision: number, confirmation: string): Promise<VaultResult> {
  return parseResult(
    await Call.ByName(`${service}.HardPurge`, expectedRevision, confirmation, correlationID()),
  );
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

function parseListResult(value: unknown): VaultListResult {
  if (!isObject(value)) {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  if (value.error !== undefined) {
    return { vaults: [], error: parseError(value.error) };
  }
  if (value.vaults === undefined) {
    return { vaults: [] };
  }
  if (!Array.isArray(value.vaults)) {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  if (value.vaults.length > 256) {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  return { vaults: value.vaults.map(parseSummary) };
}

function parseSummary(value: unknown): VaultSummary {
  if (!isObject(value) || typeof value.vault_id !== 'string' || typeof value.created_at !== 'string') {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  const createdAt = new Date(value.created_at);
  if (!uuidV7.test(value.vault_id) || Number.isNaN(createdAt.getTime())) {
    throw new Error('VAULT_RESPONSE_INVALID');
  }
  return { vault_id: value.vault_id, created_at: value.created_at };
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
  if (typeof value.vault_id === 'string' && uuidV7.test(value.vault_id)) status.vault_id = value.vault_id;
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
