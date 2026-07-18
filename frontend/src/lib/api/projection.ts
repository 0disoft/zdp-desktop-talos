import { Call } from '@wailsio/runtime';
import type { TalosError } from './vault';

const service = 'github.com/0disoft/zdp-desktop-talos/internal/transport/wailsapi.ProjectionService';

export type ProjectionFile = {
  path: string;
  media_type: string;
  sha256: string;
  size_bytes: number;
  preview: string;
  truncated: boolean;
};

export type ProjectionPreview = {
  schema: 'talos.memory-projection/1';
  bundle_sha256: string;
  included: number;
  excluded_sensitivity: number;
  excluded_lifecycle: number;
  complete: boolean;
  files: ProjectionFile[];
};

export type ProjectionPreviewResult = ProjectionPreview & { error?: TalosError };

export async function previewMemoryProjection(): Promise<ProjectionPreviewResult> {
  return parsePreview(await Call.ByName(`${service}.Preview`, correlationID()));
}

function parsePreview(value: unknown): ProjectionPreviewResult {
  if (!isObject(value)) throw new Error('PROJECTION_RESPONSE_INVALID');
  if (value.error !== undefined) {
    return { schema: 'talos.memory-projection/1', bundle_sha256: '', included: 0, excluded_sensitivity: 0, excluded_lifecycle: 0, complete: false, files: [], error: parseError(value.error) };
  }
  if (value.schema !== 'talos.memory-projection/1' || !sha256(value.bundle_sha256) || !boundedInteger(value.included, 200) || !boundedInteger(value.excluded_sensitivity, 200) || !boundedInteger(value.excluded_lifecycle, 200) || typeof value.complete !== 'boolean' || !Array.isArray(value.files) || value.files.length !== 3) throw new Error('PROJECTION_RESPONSE_INVALID');
  return {
    schema: value.schema,
    bundle_sha256: value.bundle_sha256 as string,
    included: value.included as number,
    excluded_sensitivity: value.excluded_sensitivity as number,
    excluded_lifecycle: value.excluded_lifecycle as number,
    complete: value.complete,
    files: value.files.map(parseFile),
  };
}

function parseFile(value: unknown): ProjectionFile {
  if (!isObject(value) || typeof value.path !== 'string' || !/^memory\/(memories\.jsonl|memories\.md|memories\.yaml)$/.test(value.path) || typeof value.media_type !== 'string' || !value.media_type || !sha256(value.sha256) || !boundedInteger(value.size_bytes, 64 << 20) || typeof value.preview !== 'string' || value.preview.length > 65_536 || typeof value.truncated !== 'boolean') throw new Error('PROJECTION_RESPONSE_INVALID');
  return value as ProjectionFile;
}

function boundedInteger(value: unknown, max: number): value is number { return Number.isSafeInteger(value) && (value as number) >= 0 && (value as number) <= max; }
function sha256(value: unknown): value is string { return typeof value === 'string' && /^[a-f0-9]{64}$/.test(value); }
function parseError(value: unknown): TalosError { if (!isObject(value) || typeof value.code !== 'string' || typeof value.message !== 'string' || typeof value.retryable !== 'boolean' || typeof value.correlation_id !== 'string') throw new Error('PROJECTION_RESPONSE_INVALID'); return { code: value.code, message: value.message, retryable: value.retryable, correlation_id: value.correlation_id }; }
function isObject(value: unknown): value is Record<string, unknown> { return typeof value === 'object' && value !== null && !Array.isArray(value); }
function correlationID(): string { return typeof crypto.randomUUID === 'function' ? crypto.randomUUID() : `ui-${Date.now()}-${Math.random().toString(16).slice(2)}`; }
