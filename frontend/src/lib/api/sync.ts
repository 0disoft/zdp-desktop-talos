import { Call } from '@wailsio/runtime';
import type { TalosError, VaultStatus } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.SyncService';

export type SyncDevice = { device_id: string; key_fingerprint: string; state: 'active' | 'revoked'; revision: number; next_sequence: string; local: boolean; created_at: string; updated_at: string };
export type SyncEnrollment = { enrollment_id: string; role: 'issuer' | 'recipient'; state: 'offered' | 'accepted' | 'completed' | 'canceled' | 'expired'; peer_device_id?: string; expires_at: string; updated_at: string };
export type SyncReplay = { pack_id: string; device_id: string; sequence_start: string; sequence_end: string; applied_count: number; conflicted_count: number; quarantined_count: number; reason_codes: string[]; completed_at: string };
export type SyncWorkspace = { task_id: string; workspace_id: string; baseline_commit: string; mapped: boolean; local_root?: string; state?: 'active' | 'revoked'; revision?: number; verified_baseline?: string };
export type SyncOverview = { schema: 'talos.sync-overview/1'; initialized: boolean; local_device_id: string; devices: SyncDevice[]; enrollments: SyncEnrollment[]; replays: SyncReplay[]; workspaces: SyncWorkspace[] };
export type OverviewResult = { overview?: SyncOverview; error?: TalosError };
export type AckResult = { ok: boolean; error?: TalosError };
export type OfferResult = { enrollment_id?: string; encoded?: string; secret?: string; expires_at?: string; error?: TalosError };
export type AcceptanceResult = { enrollment_id?: string; encoded_acceptance?: string; source_device_id?: string; target_device_id?: string; replay: boolean; vault?: VaultStatus; error?: TalosError };
export type FolderResult = { relative_path?: string; sequence_start?: string; sequence_end?: string; pack_replay: boolean; file_replay: boolean; imported: number; replayed: number; applied: number; conflicted: number; quarantined: number; error?: TalosError };

export async function getSyncOverview(): Promise<OverviewResult> { return parseOverviewResult(await Call.ByName(`${service}.Overview`, correlationID())); }
export async function initializeSync(): Promise<AckResult> { return parseAck(await Call.ByName(`${service}.Initialize`, correlationID())); }
export async function createEnrollmentOffer(validMinutes: number): Promise<OfferResult> { return parseOffer(await Call.ByName(`${service}.CreateEnrollmentOffer`, validMinutes, correlationID())); }
export async function acceptEnrollmentOffer(encoded: string, secret: string): Promise<AcceptanceResult> { return parseAcceptance(await Call.ByName(`${service}.AcceptEnrollmentOffer`, encoded, secret, correlationID())); }
export async function completeEnrollment(encoded: string, secret: string): Promise<AckResult> { return parseAck(await Call.ByName(`${service}.CompleteEnrollment`, encoded, secret, correlationID())); }
export async function cancelEnrollment(enrollmentID: string): Promise<AckResult> { return parseAck(await Call.ByName(`${service}.CancelEnrollment`, enrollmentID, correlationID())); }
export async function revokeSyncDevice(deviceID: string, expectedRevision: number): Promise<AckResult> { return parseAck(await Call.ByName(`${service}.RevokeDevice`, deviceID, expectedRevision, correlationID(), correlationID())); }
export async function exportSyncFolder(root: string, limit = 64): Promise<FolderResult> { return parseFolder(await Call.ByName(`${service}.ExportFolder`, root, limit, correlationID())); }
export async function importSyncFolder(root: string, deviceID: string): Promise<FolderResult> { return parseFolder(await Call.ByName(`${service}.ImportFolder`, root, deviceID, correlationID())); }
export async function mapSyncWorkspace(taskID: string, localPath: string, expectedRevision: number): Promise<AckResult> { return parseAck(await Call.ByName(`${service}.MapWorkspace`, taskID, localPath, expectedRevision, correlationID(), correlationID())); }
export async function revokeSyncWorkspace(workspaceID: string, expectedRevision: number): Promise<AckResult> { return parseAck(await Call.ByName(`${service}.RevokeWorkspace`, workspaceID, expectedRevision, correlationID(), correlationID())); }

function parseOverviewResult(value: unknown): OverviewResult {
  if (!isObject(value)) throw new Error('SYNC_RESPONSE_INVALID');
  if (value.error !== undefined) return { error: parseError(value.error) };
  if (!isObject(value.overview) || value.overview.schema !== 'talos.sync-overview/1' || typeof value.overview.initialized !== 'boolean' || typeof value.overview.local_device_id !== 'string' || !Array.isArray(value.overview.devices) || !Array.isArray(value.overview.enrollments) || !Array.isArray(value.overview.replays) || !Array.isArray(value.overview.workspaces)) throw new Error('SYNC_RESPONSE_INVALID');
  if (value.overview.devices.length > 100 || value.overview.enrollments.length > 100 || value.overview.replays.length > 50 || value.overview.workspaces.length > 100) throw new Error('SYNC_RESPONSE_INVALID');
  const devices = value.overview.devices.map(parseDevice);
  const localCount = devices.filter((item) => item.local).length;
  if ((value.overview.initialized && (value.overview.local_device_id.length === 0 || localCount !== 1)) || (!value.overview.initialized && (value.overview.local_device_id !== '' || localCount !== 0))) throw new Error('SYNC_RESPONSE_INVALID');
  return { overview: { schema: 'talos.sync-overview/1', initialized: value.overview.initialized, local_device_id: value.overview.local_device_id, devices, enrollments: value.overview.enrollments.map(parseEnrollment), replays: value.overview.replays.map(parseReplay), workspaces: value.overview.workspaces.map(parseWorkspace) } };
}

function parseDevice(value: unknown): SyncDevice {
  if (!isObject(value) || typeof value.device_id !== 'string' || !hex64(value.key_fingerprint) || (value.state !== 'active' && value.state !== 'revoked') || !positiveInteger(value.revision) || !decimal(value.next_sequence) || typeof value.local !== 'boolean' || !date(value.created_at) || !date(value.updated_at)) throw new Error('SYNC_RESPONSE_INVALID');
  return value as SyncDevice;
}

function parseEnrollment(value: unknown): SyncEnrollment {
  if (!isObject(value) || typeof value.enrollment_id !== 'string' || (value.role !== 'issuer' && value.role !== 'recipient') || !['offered', 'accepted', 'completed', 'canceled', 'expired'].includes(String(value.state)) || (value.peer_device_id !== undefined && typeof value.peer_device_id !== 'string') || !date(value.expires_at) || !date(value.updated_at)) throw new Error('SYNC_RESPONSE_INVALID');
  return value as SyncEnrollment;
}

function parseReplay(value: unknown): SyncReplay {
  if (!isObject(value) || typeof value.pack_id !== 'string' || typeof value.device_id !== 'string' || !decimal(value.sequence_start) || !decimal(value.sequence_end) || !decimalLTE(value.sequence_start, value.sequence_end) || !count(value.applied_count, 512) || !count(value.conflicted_count, 512) || !count(value.quarantined_count, 512) || !Array.isArray(value.reason_codes) || value.reason_codes.length > 8 || !value.reason_codes.every((item) => typeof item === 'string' && item.length > 0 && item.length <= 64) || !date(value.completed_at)) throw new Error('SYNC_RESPONSE_INVALID');
  return value as SyncReplay;
}

function parseWorkspace(value: unknown): SyncWorkspace {
  if (!isObject(value) || typeof value.task_id !== 'string' || typeof value.workspace_id !== 'string' || !commit(value.baseline_commit) || typeof value.mapped !== 'boolean') throw new Error('SYNC_RESPONSE_INVALID');
  if (value.mapped && (typeof value.local_root !== 'string' || value.state !== 'active' || !positiveInteger(value.revision) || !commit(value.verified_baseline))) throw new Error('SYNC_RESPONSE_INVALID');
  return value as SyncWorkspace;
}

function parseAck(value: unknown): AckResult {
  if (!isObject(value) || typeof value.ok !== 'boolean') throw new Error('SYNC_RESPONSE_INVALID');
  if (value.error !== undefined) return { ok: false, error: parseError(value.error) };
  if (!value.ok) throw new Error('SYNC_RESPONSE_INVALID');
  return { ok: true };
}

function parseOffer(value: unknown): OfferResult {
  if (!isObject(value)) throw new Error('SYNC_RESPONSE_INVALID');
  if (value.error !== undefined) return { error: parseError(value.error) };
  if (typeof value.enrollment_id !== 'string' || !base64(value.encoded, 131_072) || typeof value.secret !== 'string' || value.secret.length < 16 || value.secret.length > 256 || !date(value.expires_at)) throw new Error('SYNC_RESPONSE_INVALID');
  return value as OfferResult;
}

function parseAcceptance(value: unknown): AcceptanceResult {
  if (!isObject(value) || typeof value.replay !== 'boolean') throw new Error('SYNC_RESPONSE_INVALID');
  if (value.error !== undefined) return { replay: false, error: parseError(value.error) };
  if (typeof value.enrollment_id !== 'string' || !base64(value.encoded_acceptance, 131_072) || typeof value.source_device_id !== 'string' || typeof value.target_device_id !== 'string' || !isObject(value.vault) || value.vault.state !== 'unlocked' || typeof value.vault.vault_id !== 'string') throw new Error('SYNC_RESPONSE_INVALID');
  return value as AcceptanceResult;
}

function parseFolder(value: unknown): FolderResult {
  if (!isObject(value) || typeof value.pack_replay !== 'boolean' || typeof value.file_replay !== 'boolean' || !count(value.imported, 512) || !count(value.replayed, 512) || !count(value.applied, 262_144) || !count(value.conflicted, 262_144) || !count(value.quarantined, 262_144)) throw new Error('SYNC_RESPONSE_INVALID');
  if (value.error !== undefined) return { pack_replay: false, file_replay: false, imported: 0, replayed: 0, applied: 0, conflicted: 0, quarantined: 0, error: parseError(value.error) };
  if (value.relative_path !== undefined && (typeof value.relative_path !== 'string' || value.relative_path.length > 4096 || !decimal(value.sequence_start) || !decimal(value.sequence_end) || !decimalLTE(value.sequence_start, value.sequence_end))) throw new Error('SYNC_RESPONSE_INVALID');
  return value as FolderResult;
}

function parseError(value: unknown): TalosError { if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('SYNC_RESPONSE_INVALID'); return value as TalosError; }
function isObject(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function count(value: unknown, max: number): value is number { return Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= max; }
function positiveInteger(value: unknown): value is number { return Number.isSafeInteger(value) && (value as number) > 0; }
function decimal(value: unknown): value is string { return typeof value === 'string' && /^[1-9][0-9]{0,19}$/.test(value) && decimalLTE(value, '18446744073709551615'); }
function decimalLTE(left: string, right: string): boolean { return left.length < right.length || (left.length === right.length && left <= right); }
function hex64(value: unknown): value is string { return typeof value === 'string' && /^[a-f0-9]{64}$/.test(value); }
function commit(value: unknown): value is string { return typeof value === 'string' && /^[a-f0-9]{40,64}$/.test(value); }
function date(value: unknown): value is string { return typeof value === 'string' && !Number.isNaN(new Date(value).getTime()); }
function base64(value: unknown, max: number): value is string { return typeof value === 'string' && value.length > 0 && value.length <= max && /^[A-Za-z0-9_-]+$/.test(value); }
function correlationID(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
