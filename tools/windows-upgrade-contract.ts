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

export type UpgradeStage =
  | "guard"
  | "package-verification"
  | "old-install"
  | "old-vault"
  | "new-install"
  | "new-vault"
  | "uninstall-retention"
  | "rollback"
  | "purge"
  | "cleanup";

export type UpgradeEvidencePhase = {
  name: Exclude<UpgradeStage, "cleanup">;
  status: "passed";
  application_version?: string;
  revision?: number;
  retention_days?: number;
};

export type UpgradePackageEvidence = {
  version: string;
  source_commit: string;
  receipt_sha256: string;
};

export type UpgradeVaultEvidence = {
  id: string;
  final_revision: number;
  final_retention_days: number;
  backup_id: string;
  backup_ciphertext_sha256: string;
  backup_source_schema: number;
  backup_target_schema: number;
  uninstall_retention_verified: true;
  direct_rollback_read_verified: true;
  purged: true;
};

export type UpgradeEvidence = {
  schema: typeof upgradeEvidenceSchema;
  status: "passed" | "failed";
  architecture: "amd64";
  signer_thumbprint_sha1: string;
  runner_preflight: UpgradeRunnerPreflight;
  verifier_commit: string;
  old_run_id: string;
  new_run_id: string;
  old_package?: UpgradePackageEvidence;
  new_package?: UpgradePackageEvidence;
  vault?: UpgradeVaultEvidence;
  phases: UpgradeEvidencePhase[];
  failure?: { stage: UpgradeStage; code: string };
};

export type UpgradeEvidenceExpectations = {
  expectedSignerThumbprint: string;
  verifierCommit: string;
  oldRunID: string;
  newRunID: string;
  oldSourceCommit: string;
  newSourceCommit: string;
  requirePassed: boolean;
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
const upgradeEvidenceKeys = new Set([
  "schema",
  "status",
  "architecture",
  "signer_thumbprint_sha1",
  "runner_preflight",
  "verifier_commit",
  "old_run_id",
  "new_run_id",
  "old_package",
  "new_package",
  "vault",
  "phases",
  "failure",
]);
const upgradePackageKeys = ["version", "source_commit", "receipt_sha256"] as const;
const upgradeVaultKeys = [
  "id",
  "final_revision",
  "final_retention_days",
  "backup_id",
  "backup_ciphertext_sha256",
  "backup_source_schema",
  "backup_target_schema",
  "uninstall_retention_verified",
  "direct_rollback_read_verified",
  "purged",
] as const;
const upgradePhaseKeys = new Set(["name", "status", "application_version", "revision", "retention_days"]);
const upgradeFailureKeys = ["stage", "code"] as const;
const upgradePhaseOrder = [
  "guard",
  "package-verification",
  "old-install",
  "old-vault",
  "new-install",
  "new-vault",
  "uninstall-retention",
  "rollback",
  "purge",
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
const runIDPattern = /^[1-9]\d*$/;
const failureCodePattern = /^[A-Z][A-Z0-9_]{2,63}$/;
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

function rejectUnknownKeys(value: Record<string, unknown>, allowed: ReadonlySet<string>, label: string): void {
  for (const key of Object.keys(value)) {
    if (!allowed.has(key)) {
      throw new Error(`${label} contains unsupported field: ${key}`);
    }
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
  if (typeof expectedSignerThumbprint !== "string") {
    throw new Error("expected upgrade signer thumbprint is invalid");
  }
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

function validateUpgradePackage(value: unknown, expectedCommit: string, label: string): UpgradePackageEvidence {
  const package_ = record(value, label);
  exactKeys(package_, upgradePackageKeys, label);
  if (typeof package_.version !== "string") {
    throw new Error(`${label} version is invalid`);
  }
  parseVersion(package_.version);
  if (package_.source_commit !== expectedCommit || !commitPattern.test(expectedCommit)) {
    throw new Error(`${label} source commit does not match the trusted signing run`);
  }
  if (typeof package_.receipt_sha256 !== "string" || !sha256Pattern.test(package_.receipt_sha256)) {
    throw new Error(`${label} receipt hash is invalid`);
  }
  return { version: package_.version, source_commit: expectedCommit, receipt_sha256: package_.receipt_sha256 };
}

function validateUpgradeVault(value: unknown): UpgradeVaultEvidence {
  const vault = record(value, "upgrade evidence Vault");
  exactKeys(vault, upgradeVaultKeys, "upgrade evidence Vault");
  if (
    typeof vault.id !== "string" ||
    vault.id.trim() === "" ||
    vault.id.length > 128 ||
    typeof vault.backup_id !== "string" ||
    vault.backup_id.trim() === "" ||
    vault.backup_id.length > 128 ||
    typeof vault.backup_ciphertext_sha256 !== "string" ||
    !sha256Pattern.test(vault.backup_ciphertext_sha256) ||
    vault.final_revision !== 2 ||
    vault.final_retention_days !== 45 ||
    vault.uninstall_retention_verified !== true ||
    vault.direct_rollback_read_verified !== true ||
    vault.purged !== true
  ) {
    throw new Error("upgrade evidence Vault does not satisfy the N-1 proof contract");
  }
  return {
    id: vault.id,
    final_revision: 2,
    final_retention_days: 45,
    backup_id: vault.backup_id,
    backup_ciphertext_sha256: vault.backup_ciphertext_sha256,
    backup_source_schema: positiveInteger(vault.backup_source_schema, "upgrade evidence backup source schema"),
    backup_target_schema: positiveInteger(vault.backup_target_schema, "upgrade evidence backup target schema"),
    uninstall_retention_verified: true,
    direct_rollback_read_verified: true,
    purged: true,
  };
}

function validateUpgradePhases(value: unknown): UpgradeEvidencePhase[] {
  if (!Array.isArray(value) || value.length > upgradePhaseOrder.length) {
    throw new Error("upgrade evidence phases are invalid");
  }
  return value.map((item, index) => {
    const phase = record(item, `upgrade evidence phase ${index}`);
    rejectUnknownKeys(phase, upgradePhaseKeys, `upgrade evidence phase ${index}`);
    if (phase.name !== upgradePhaseOrder[index] || phase.status !== "passed") {
      throw new Error("upgrade evidence phases are not the exact successful prefix");
    }
    const result: UpgradeEvidencePhase = { name: upgradePhaseOrder[index], status: "passed" };
    if (phase.application_version !== undefined) {
      if (typeof phase.application_version !== "string") {
        throw new Error(`upgrade evidence phase ${index} application version is invalid`);
      }
      parseVersion(phase.application_version);
      result.application_version = phase.application_version;
    }
    if (phase.revision !== undefined) {
      result.revision = positiveInteger(phase.revision, `upgrade evidence phase ${index} revision`);
    }
    if (phase.retention_days !== undefined) {
      result.retention_days = positiveInteger(phase.retention_days, `upgrade evidence phase ${index} retention`);
    }
    return result;
  });
}

function requireExactPhasePrefix(phases: UpgradeEvidencePhase[], oldVersion: string, newVersion: string, requireComplete: boolean): void {
  const expected: UpgradeEvidencePhase[] = [
    { name: "guard", status: "passed" },
    { name: "package-verification", status: "passed" },
    { name: "old-install", status: "passed", application_version: oldVersion },
    { name: "old-vault", status: "passed", application_version: oldVersion, revision: 1, retention_days: 30 },
    { name: "new-install", status: "passed", application_version: newVersion },
    { name: "new-vault", status: "passed", application_version: newVersion, revision: 2, retention_days: 45 },
    { name: "uninstall-retention", status: "passed", revision: 2, retention_days: 45 },
    { name: "rollback", status: "passed", application_version: oldVersion, revision: 2, retention_days: 45 },
    { name: "purge", status: "passed", revision: 2 },
  ];
  if ((requireComplete && phases.length !== expected.length) || JSON.stringify(phases) !== JSON.stringify(expected.slice(0, phases.length))) {
    throw new Error("upgrade evidence does not contain the exact successful N-1 phase prefix");
  }
}

export function validateUpgradeEvidence(value: unknown, expected: UpgradeEvidenceExpectations): UpgradeEvidence {
  const evidence = record(value, "upgrade evidence");
  rejectUnknownKeys(evidence, upgradeEvidenceKeys, "upgrade evidence");
  if (typeof expected.expectedSignerThumbprint !== "string" || typeof expected.requirePassed !== "boolean") {
    throw new Error("upgrade evidence verification policy is invalid");
  }
  const normalizedSigner = expected.expectedSignerThumbprint.toLowerCase();
  if (
    evidence.schema !== upgradeEvidenceSchema ||
    (evidence.status !== "passed" && evidence.status !== "failed") ||
    evidence.architecture !== "amd64" ||
    typeof evidence.signer_thumbprint_sha1 !== "string" ||
    !signerThumbprintPattern.test(evidence.signer_thumbprint_sha1) ||
    evidence.signer_thumbprint_sha1 !== normalizedSigner ||
    !signerThumbprintPattern.test(normalizedSigner) ||
    evidence.verifier_commit !== expected.verifierCommit ||
    !commitPattern.test(expected.verifierCommit) ||
    evidence.old_run_id !== expected.oldRunID ||
    evidence.new_run_id !== expected.newRunID ||
    !runIDPattern.test(expected.oldRunID) ||
    !runIDPattern.test(expected.newRunID) ||
    expected.oldRunID === expected.newRunID ||
    !commitPattern.test(expected.oldSourceCommit) ||
    !commitPattern.test(expected.newSourceCommit) ||
    expected.oldSourceCommit === expected.newSourceCommit
  ) {
    throw new Error("upgrade evidence identity does not match the trusted run selection");
  }
  const runnerPreflight = validateUpgradeRunnerPreflight(evidence.runner_preflight, normalizedSigner);
  const phases = validateUpgradePhases(evidence.phases);
  const oldPackage = evidence.old_package === undefined ? undefined : validateUpgradePackage(evidence.old_package, expected.oldSourceCommit, "old package evidence");
  const newPackage = evidence.new_package === undefined ? undefined : validateUpgradePackage(evidence.new_package, expected.newSourceCommit, "new package evidence");
  if ((oldPackage === undefined) !== (newPackage === undefined)) {
    throw new Error("upgrade evidence must contain both package identities or neither");
  }
  if (oldPackage && newPackage && compareVersions(oldPackage.version, newPackage.version) >= 0) {
    throw new Error("upgrade evidence package versions are not strictly increasing");
  }
  if ((phases.length >= 2) !== (oldPackage !== undefined && newPackage !== undefined)) {
    throw new Error("upgrade evidence package identities do not match the completed phase prefix");
  }
  if (oldPackage && newPackage) {
    requireExactPhasePrefix(phases, oldPackage.version, newPackage.version, evidence.status === "passed");
  } else if (JSON.stringify(phases) !== JSON.stringify(upgradePhaseOrder.slice(0, phases.length).map((name) => ({ name, status: "passed" })))) {
    throw new Error("upgrade evidence early phase prefix contains unsupported details");
  }

  const result: UpgradeEvidence = {
    schema: upgradeEvidenceSchema,
    status: evidence.status,
    architecture: "amd64",
    signer_thumbprint_sha1: normalizedSigner,
    runner_preflight: runnerPreflight,
    verifier_commit: expected.verifierCommit,
    old_run_id: expected.oldRunID,
    new_run_id: expected.newRunID,
    phases,
  };
  if (oldPackage && newPackage) {
    result.old_package = oldPackage;
    result.new_package = newPackage;
  }

  if (evidence.status === "passed") {
    if (!oldPackage || !newPackage || evidence.failure !== undefined || evidence.vault === undefined) {
      throw new Error("upgrade evidence is not a complete passing promotion artifact");
    }
    result.vault = validateUpgradeVault(evidence.vault);
    return result;
  }

  if (expected.requirePassed) {
    throw new Error("upgrade evidence records a failed native run");
  }
  if (evidence.vault !== undefined || evidence.failure === undefined) {
    throw new Error("failed upgrade evidence has an invalid terminal shape");
  }
  const failure = record(evidence.failure, "upgrade evidence failure");
  exactKeys(failure, upgradeFailureKeys, "upgrade evidence failure");
  if (
    typeof failure.stage !== "string" ||
    !new Set<UpgradeStage>([...upgradePhaseOrder, "cleanup"]).has(failure.stage as UpgradeStage) ||
    typeof failure.code !== "string" ||
    !failureCodePattern.test(failure.code)
  ) {
    throw new Error("upgrade evidence failure is invalid");
  }
  const failureIndex = failure.stage === "cleanup" ? upgradePhaseOrder.length : upgradePhaseOrder.indexOf(failure.stage as (typeof upgradePhaseOrder)[number]);
  if (failureIndex < phases.length) {
    throw new Error("upgrade evidence failure precedes an already passed phase");
  }
  result.failure = { stage: failure.stage as UpgradeStage, code: failure.code };
  return result;
}

export async function readAndValidateUpgradeEvidence(
  evidencePath: string,
  expected: UpgradeEvidenceExpectations,
): Promise<{ evidence: UpgradeEvidence; sha256: string }> {
  const stat = await lstat(evidencePath);
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size <= 0 || stat.size > 64 * 1024) {
    throw new Error("upgrade evidence must be a bounded regular file");
  }
  const raw = await readFile(evidencePath);
  if (raw.byteLength !== stat.size || raw.byteLength > 64 * 1024) {
    throw new Error("upgrade evidence changed during bounded read");
  }
  return {
    evidence: validateUpgradeEvidence(JSON.parse(new TextDecoder("utf-8", { fatal: true }).decode(raw)) as unknown, expected),
    sha256: createHash("sha256").update(raw).digest("hex"),
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
