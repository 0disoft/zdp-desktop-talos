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

export type VaultBackupReceipt = {
  schema: 'talos.vault-backup-receipt/1';
  backup_id: string;
  vault_id: string;
  path: string;
  source_application_version: string;
  source_schema_version: number;
  created_at: string;
  encrypted_size_bytes: number;
  ciphertext_sha256: string;
  database_size_bytes: number;
  artifact_count: number;
  event_count: number;
};

export type VaultBackupPreflight = {
  schema: 'talos.vault-backup-preflight/1';
  backup_id: string;
  vault_id: string;
  path: string;
  source_application_version: string;
  minimum_restore_version: string;
  target_application_version: string;
  source_schema_version: number;
  target_schema_version: number;
  migration_required: boolean;
  created_at: string;
  verified_at: string;
  encrypted_size_bytes: number;
  ciphertext_sha256: string;
  database_size_bytes: number;
  artifact_count: number;
  event_count: number;
};

export type VaultRestore = {
  schema: 'talos.vault-backup-restore/1';
  state: 'restored' | 'rolled_back';
  backup_id: string;
  vault_id: string;
  ciphertext_sha256: string;
  source_application_version: string;
  target_application_version: string;
  source_schema_version: number;
  target_schema_version: number;
  restored_at: string;
  artifact_count: number;
  event_count: number;
};

export type VaultBackupResult = { receipt?: VaultBackupReceipt; error?: TalosError };
export type VaultBackupPreflightResult = { preflight?: VaultBackupPreflight; error?: TalosError };
export type VaultRestoreResult = { restore?: VaultRestore; vault?: VaultStatus; error?: TalosError };

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

export async function createVaultBackup(destination: string): Promise<VaultBackupResult> {
  if (!validLocalPath(destination)) throw new Error('VAULT_BACKUP_PATH_INVALID');
  return parseBackupResult(await Call.ByName(`${service}.CreateBackup`, destination, correlationID()));
}

export async function preflightVaultBackup(source: string): Promise<VaultBackupPreflightResult> {
  if (!validLocalPath(source)) throw new Error('VAULT_BACKUP_PATH_INVALID');
  return parseBackupPreflightResult(await Call.ByName(`${service}.PreflightBackup`, source, correlationID()));
}

export async function restoreVaultBackup(
  source: string,
  expectedBackupID: string,
  expectedCiphertextSHA256: string,
  expectedRevision: number,
  confirmation: string,
): Promise<VaultRestoreResult> {
  if (!validLocalPath(source) || !uuidV7.test(expectedBackupID) || !/^[0-9a-f]{64}$/.test(expectedCiphertextSHA256) || !validPositiveInteger(expectedRevision) || !uuidV7.test(confirmation)) {
    throw new Error('VAULT_RESTORE_REQUEST_INVALID');
  }
  return parseRestoreResult(await Call.ByName(
    `${service}.RestoreBackup`, source, expectedBackupID, expectedCiphertextSHA256, expectedRevision, confirmation, correlationID(),
  ));
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

function parseBackupResult(value: unknown): VaultBackupResult {
  if (!isObject(value)) throw new Error('VAULT_BACKUP_RESPONSE_INVALID');
  const result: VaultBackupResult = {};
  if (value.receipt !== undefined) result.receipt = parseBackupReceipt(value.receipt);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.receipt === undefined) === (result.error === undefined)) throw new Error('VAULT_BACKUP_RESPONSE_INVALID');
  return result;
}

function parseBackupPreflightResult(value: unknown): VaultBackupPreflightResult {
  if (!isObject(value)) throw new Error('VAULT_BACKUP_RESPONSE_INVALID');
  const result: VaultBackupPreflightResult = {};
  if (value.preflight !== undefined) result.preflight = parseBackupPreflight(value.preflight);
  if (value.error !== undefined) result.error = parseError(value.error);
  if ((result.preflight === undefined) === (result.error === undefined)) throw new Error('VAULT_BACKUP_RESPONSE_INVALID');
  return result;
}

function parseRestoreResult(value: unknown): VaultRestoreResult {
  if (!isObject(value)) throw new Error('VAULT_RESTORE_RESPONSE_INVALID');
  const result: VaultRestoreResult = {};
  if (value.restore !== undefined) result.restore = parseRestore(value.restore);
  if (value.vault !== undefined) result.vault = parseStatus(value.vault);
  if (value.error !== undefined) result.error = parseError(value.error);
  if (result.restore !== undefined && result.vault === undefined) throw new Error('VAULT_RESTORE_RESPONSE_INVALID');
  if (result.error === undefined && (result.restore === undefined || result.vault === undefined)) throw new Error('VAULT_RESTORE_RESPONSE_INVALID');
  if (result.restore === undefined && result.vault !== undefined && result.error === undefined) throw new Error('VAULT_RESTORE_RESPONSE_INVALID');
  return result;
}

function parseBackupReceipt(value: unknown): VaultBackupReceipt {
  if (
    !validBackupCommon(value) ||
    value.schema !== 'talos.vault-backup-receipt/1' ||
    !validPositiveInteger(value.source_schema_version)
  ) {
    throw new Error('VAULT_BACKUP_RESPONSE_INVALID');
  }
  return value as VaultBackupReceipt;
}

function parseBackupPreflight(value: unknown): VaultBackupPreflight {
  if (
    !validBackupCommon(value) ||
    value.schema !== 'talos.vault-backup-preflight/1' ||
    !validPositiveInteger(value.source_schema_version) ||
    typeof value.target_application_version !== 'string' || !validVersion(value.target_application_version) ||
    typeof value.minimum_restore_version !== 'string' || !validVersion(value.minimum_restore_version) ||
    !validPositiveInteger(value.target_schema_version) ||
    typeof value.migration_required !== 'boolean' ||
    typeof value.verified_at !== 'string' || !validDate(value.verified_at)
  ) {
    throw new Error('VAULT_BACKUP_RESPONSE_INVALID');
  }
  return value as VaultBackupPreflight;
}

function parseRestore(value: unknown): VaultRestore {
  if (
    !isObject(value) || value.schema !== 'talos.vault-backup-restore/1' ||
    (value.state !== 'restored' && value.state !== 'rolled_back') ||
    typeof value.backup_id !== 'string' || !uuidV7.test(value.backup_id) ||
    typeof value.vault_id !== 'string' || !uuidV7.test(value.vault_id) ||
    typeof value.ciphertext_sha256 !== 'string' || !/^[0-9a-f]{64}$/.test(value.ciphertext_sha256) ||
    typeof value.source_application_version !== 'string' || !validVersion(value.source_application_version) ||
    typeof value.target_application_version !== 'string' || !validVersion(value.target_application_version) ||
    !validPositiveInteger(value.source_schema_version) || !validPositiveInteger(value.target_schema_version) ||
    typeof value.restored_at !== 'string' || !validDate(value.restored_at) ||
    !validNonNegativeInteger(value.artifact_count) || !validPositiveInteger(value.event_count)
  ) {
    throw new Error('VAULT_RESTORE_RESPONSE_INVALID');
  }
  return value as VaultRestore;
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

function validLocalPath(value: string): boolean {
  return value.trim() === value && value.length > 0 && value.length <= 32767 && !value.includes('\0');
}

function validBackupCommon(value: unknown): value is Record<string, unknown> {
  return (
    isObject(value) &&
    typeof value.backup_id === 'string' && uuidV7.test(value.backup_id) &&
    typeof value.vault_id === 'string' && uuidV7.test(value.vault_id) &&
    typeof value.path === 'string' && validLocalPath(value.path) &&
    typeof value.source_application_version === 'string' && validVersion(value.source_application_version) &&
    typeof value.created_at === 'string' && validDate(value.created_at) &&
    validPositiveInteger(value.encrypted_size_bytes) &&
    typeof value.ciphertext_sha256 === 'string' && /^[0-9a-f]{64}$/.test(value.ciphertext_sha256) &&
    validPositiveInteger(value.database_size_bytes) &&
    validNonNegativeInteger(value.artifact_count) &&
    validPositiveInteger(value.event_count)
  );
}

function validVersion(value: string): boolean {
  return /^(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)$/.test(value);
}

function validDate(value: string): boolean {
  return !Number.isNaN(new Date(value).getTime());
}

function validPositiveInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value > 0;
}

function validNonNegativeInteger(value: unknown): value is number {
  return typeof value === 'number' && Number.isSafeInteger(value) && value >= 0;
}

function correlationID(): string {
  return typeof crypto.randomUUID === 'function'
    ? crypto.randomUUID()
    : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`;
}
