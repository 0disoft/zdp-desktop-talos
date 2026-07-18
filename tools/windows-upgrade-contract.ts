import { createHash } from "node:crypto";
import { lstat, readFile, readdir } from "node:fs/promises";
import path from "node:path";

export const packageReceiptSchema = "talos.windows-package-receipt/1";
export const releaseProbeSchema = "talos.release-probe/1";
export const upgradeEvidenceSchema = "talos.windows-upgrade-evidence/1";

export type ArtifactReceipt = {
  name: string;
  sha256: string;
  size: number;
  signature_status: "Valid";
  signer_subject: string;
};

export type PackageReceipt = {
  schema: typeof packageReceiptSchema;
  version: string;
  architecture: "amd64";
  source_commit: string;
  signature_required: true;
  release_probe_schema: typeof releaseProbeSchema;
  artifacts: ArtifactReceipt[];
};

export type ProbeReport = {
  schema: typeof releaseProbeSchema;
  action: "create" | "inspect" | "update-retention" | "backup" | "restore" | "purge";
  application_version: string;
  vault_id: string;
  revision: number;
  retention_days: number;
  backup_id?: string;
  backup_ciphertext_sha256?: string;
  backup_source_schema?: number;
  backup_target_schema?: number;
  restore_state?: string;
};

export type UpgradeRunnerPreflight = {
  schema: "talos.windows-upgrade-runner-preflight/1";
  architecture: "amd64";
  bun_version: string;
  signer_thumbprint_sha1: string;
  webview2_ready: true;
  signing_private_keys_absent: true;
  installation_absent: true;
  data_absent: true;
};

const receiptKeys = [
  "schema",
  "version",
  "architecture",
  "source_commit",
  "signature_required",
  "release_probe_schema",
  "artifacts",
] as const;
const artifactKeys = ["name", "sha256", "size", "signature_status", "signer_subject"] as const;
const runnerPreflightKeys = [
  "schema",
  "architecture",
  "bun_version",
  "signer_thumbprint_sha1",
  "webview2_ready",
  "signing_private_keys_absent",
  "installation_absent",
  "data_absent",
] as const;
const probeKeys = new Set([
  "schema",
  "action",
  "application_version",
  "vault_id",
  "revision",
  "retention_days",
  "backup_id",
  "backup_ciphertext_sha256",
  "backup_source_schema",
  "backup_target_schema",
  "restore_state",
]);
const sha256Pattern = /^[a-f0-9]{64}$/;
const commitPattern = /^[a-f0-9]{40}$/;
const signerThumbprintPattern = /^[a-f0-9]{40}$/;
const versionPattern = /^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)$/;

function record(value: unknown, label: string): Record<string, unknown> {
  if (value === null || typeof value !== "object" || Array.isArray(value)) {
    throw new Error(`${label} must be a JSON object`);
  }
  return value as Record<string, unknown>;
}

function exactKeys(value: Record<string, unknown>, expected: readonly string[], label: string): void {
  const actual = Object.keys(value).sort();
  const wanted = [...expected].sort();
  if (actual.length !== wanted.length || actual.some((key, index) => key !== wanted[index])) {
    throw new Error(`${label} has an unsupported field set`);
  }
}

function positiveInteger(value: unknown, label: string): number {
  if (!Number.isSafeInteger(value) || (value as number) < 1) {
    throw new Error(`${label} must be a positive safe integer`);
  }
  return value as number;
}

export function compareVersions(left: string, right: string): number {
  const leftParts = parseVersion(left);
  const rightParts = parseVersion(right);
  for (let index = 0; index < leftParts.length; index += 1) {
    const difference = leftParts[index] - rightParts[index];
    if (difference !== 0) {
      return Math.sign(difference);
    }
  }
  return 0;
}

function parseVersion(value: string): [number, number, number] {
  const match = versionPattern.exec(value);
  if (!match) {
    throw new Error(`invalid semantic version: ${value}`);
  }
  return [Number(match[1]), Number(match[2]), Number(match[3])];
}

export function validateReceipt(value: unknown, expectedSourceCommit: string): PackageReceipt {
  const receipt = record(value, "package receipt");
  exactKeys(receipt, receiptKeys, "package receipt");
  if (receipt.schema !== packageReceiptSchema) {
    throw new Error("package receipt schema is unsupported");
  }
  if (typeof receipt.version !== "string") {
    throw new Error("package receipt version is invalid");
  }
  parseVersion(receipt.version);
  if (receipt.architecture !== "amd64" || receipt.signature_required !== true || receipt.release_probe_schema !== releaseProbeSchema) {
    throw new Error("package receipt platform or signing contract is invalid");
  }
  if (!commitPattern.test(expectedSourceCommit) || receipt.source_commit !== expectedSourceCommit) {
    throw new Error("package receipt source commit does not match the trusted workflow run");
  }
  if (!Array.isArray(receipt.artifacts) || receipt.artifacts.length !== 5) {
    throw new Error("package receipt must contain exactly five signed artifacts");
  }

  const installerName = `talos-agent-${receipt.version}-windows-amd64-setup.exe`;
  const expectedNames = new Set([
    "talos-desktop.exe",
    "talos-worker.exe",
    "talosctl.exe",
    "talos-release-probe.exe",
    installerName,
  ]);
  const artifacts = receipt.artifacts.map((item, index) => {
    const artifact = record(item, `package receipt artifact ${index}`);
    exactKeys(artifact, artifactKeys, `package receipt artifact ${index}`);
    if (typeof artifact.name !== "string" || path.basename(artifact.name) !== artifact.name || !expectedNames.delete(artifact.name)) {
      throw new Error("package receipt contains an unexpected or duplicate artifact name");
    }
    if (typeof artifact.sha256 !== "string" || !sha256Pattern.test(artifact.sha256)) {
      throw new Error(`package receipt hash is invalid for ${artifact.name}`);
    }
    if (artifact.signature_status !== "Valid" || typeof artifact.signer_subject !== "string" || artifact.signer_subject.trim() === "") {
      throw new Error(`package receipt signature metadata is invalid for ${artifact.name}`);
    }
    return {
      name: artifact.name,
      sha256: artifact.sha256,
      size: positiveInteger(artifact.size, `package receipt size for ${artifact.name}`),
      signature_status: "Valid" as const,
      signer_subject: artifact.signer_subject,
    };
  });
  if (expectedNames.size !== 0) {
    throw new Error("package receipt is missing a required artifact");
  }
  return {
    schema: packageReceiptSchema,
    version: receipt.version,
    architecture: "amd64",
    source_commit: expectedSourceCommit,
    signature_required: true,
    release_probe_schema: releaseProbeSchema,
    artifacts,
  };
}

export function validateProbeReport(value: unknown, expected: Partial<ProbeReport> & Pick<ProbeReport, "action" | "application_version">): ProbeReport {
  const candidate = record(value, "release probe report");
  for (const key of Object.keys(candidate)) {
    if (!probeKeys.has(key)) {
      throw new Error(`release probe report contains unsupported field: ${key}`);
    }
  }
  if (candidate.schema !== releaseProbeSchema || candidate.action !== expected.action || candidate.application_version !== expected.application_version) {
    throw new Error("release probe identity does not match the expected package and action");
  }
  if (typeof candidate.vault_id !== "string" || candidate.vault_id.trim() === "") {
    throw new Error("release probe Vault ID is invalid");
  }
  const report: ProbeReport = {
    schema: releaseProbeSchema,
    action: candidate.action as ProbeReport["action"],
    application_version: candidate.application_version as string,
    vault_id: candidate.vault_id,
    revision: positiveInteger(candidate.revision, "release probe revision"),
    retention_days: candidate.action === "purge" && candidate.retention_days === 0 ? 0 : positiveInteger(candidate.retention_days, "release probe retention"),
  };
  for (const field of ["backup_id", "backup_ciphertext_sha256", "restore_state"] as const) {
    if (candidate[field] !== undefined) {
      if (typeof candidate[field] !== "string" || candidate[field].trim() === "") {
        throw new Error(`release probe ${field} is invalid`);
      }
      report[field] = candidate[field];
    }
  }
  for (const field of ["backup_source_schema", "backup_target_schema"] as const) {
    if (candidate[field] !== undefined) {
      report[field] = positiveInteger(candidate[field], `release probe ${field}`);
    }
  }
  for (const [key, wanted] of Object.entries(expected)) {
    if (wanted !== undefined && report[key as keyof ProbeReport] !== wanted) {
      throw new Error(`release probe ${key} does not match the expected value`);
    }
  }
  return report;
}

export function validateUpgradeRunnerPreflight(value: unknown, expectedSignerThumbprint: string): UpgradeRunnerPreflight {
  const preflight = record(value, "upgrade runner preflight");
  exactKeys(preflight, runnerPreflightKeys, "upgrade runner preflight");
  const normalizedSigner = expectedSignerThumbprint.toLowerCase();
  if (
    preflight.schema !== "talos.windows-upgrade-runner-preflight/1" ||
    preflight.architecture !== "amd64" ||
    typeof preflight.bun_version !== "string" ||
    !versionPattern.test(preflight.bun_version) ||
    typeof preflight.signer_thumbprint_sha1 !== "string" ||
    !signerThumbprintPattern.test(preflight.signer_thumbprint_sha1) ||
    preflight.signer_thumbprint_sha1 !== normalizedSigner ||
    preflight.webview2_ready !== true ||
    preflight.signing_private_keys_absent !== true ||
    preflight.installation_absent !== true ||
    preflight.data_absent !== true
  ) {
    throw new Error("upgrade runner preflight does not satisfy the release boundary");
  }
  return {
    schema: "talos.windows-upgrade-runner-preflight/1",
    architecture: "amd64",
    bun_version: preflight.bun_version,
    signer_thumbprint_sha1: normalizedSigner,
    webview2_ready: true,
    signing_private_keys_absent: true,
    installation_absent: true,
    data_absent: true,
  };
}

export async function findSingleReceipt(root: string): Promise<string> {
  const matches: string[] = [];
  await walk(root, matches);
  if (matches.length !== 1) {
    throw new Error(`expected exactly one package receipt, found ${matches.length}`);
  }
  return matches[0];
}

async function walk(directory: string, matches: string[]): Promise<void> {
  const entries = await readdir(directory, { withFileTypes: true });
  for (const entry of entries) {
    const candidate = path.join(directory, entry.name);
    if (entry.isSymbolicLink()) {
      throw new Error("package artifact tree contains a symbolic link");
    }
    if (entry.isDirectory()) {
      await walk(candidate, matches);
    } else if (entry.isFile() && entry.name.endsWith("-receipt.json")) {
      matches.push(candidate);
    }
  }
}

export async function readAndValidateReceipt(receiptPath: string, expectedSourceCommit: string): Promise<PackageReceipt> {
  const raw = await readFile(receiptPath, "utf8");
  return validateReceipt(JSON.parse(raw) as unknown, expectedSourceCommit);
}

export async function verifyReceiptFiles(receiptPath: string, receipt: PackageReceipt): Promise<void> {
  const root = path.dirname(receiptPath);
  for (const artifact of receipt.artifacts) {
    const artifactPath = path.join(root, artifact.name);
    const stat = await lstat(artifactPath);
    if (!stat.isFile() || stat.isSymbolicLink() || stat.size !== artifact.size) {
      throw new Error(`package artifact size or file type is invalid for ${artifact.name}`);
    }
    if ((await sha256File(artifactPath)) !== artifact.sha256) {
      throw new Error(`package artifact hash is invalid for ${artifact.name}`);
    }
  }
}

export async function sha256File(filePath: string): Promise<string> {
  return createHash("sha256").update(await readFile(filePath)).digest("hex");
}

export function artifactPath(receiptPath: string, receipt: PackageReceipt, name: string): string {
  const artifact = receipt.artifacts.find((candidate) => candidate.name === name);
  if (!artifact) {
    throw new Error(`package artifact is missing: ${name}`);
  }
  return path.join(path.dirname(receiptPath), artifact.name);
}
