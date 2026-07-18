import { lstat, mkdir, readFile, realpath, writeFile } from "node:fs/promises";
import path from "node:path";
import {
  artifactPath,
  compareVersions,
  findSingleReceipt,
  readAndValidateReceipt,
  releaseProbeSchema,
  sha256File,
  upgradeEvidenceSchema,
  validateProbeReport,
  validateUpgradeRunnerPreflight,
  verifyReceiptFiles,
  type PackageReceipt,
  type ProbeReport,
  type UpgradeEvidence,
  type UpgradeRunnerPreflight,
  type UpgradeStage,
} from "./windows-upgrade-contract";

const maxProcessOutputBytes = 1024 * 1024;
const signerPattern = /^[A-Fa-f0-9]{40}$/;
const commitPattern = /^[a-f0-9]{40}$/;
const runIDPattern = /^[1-9]\d*$/;

type Stage = UpgradeStage;

class SmokeFailure extends Error {
  constructor(
    readonly stage: Stage,
    readonly code: string,
  ) {
    super(`${stage}:${code}`);
  }
}

type PackageContext = {
  receiptPath: string;
  receipt: PackageReceipt;
  receiptSHA256: string;
  installerPath: string;
  probePath: string;
};

export function argumentsMap(arguments_: string[]): Map<string, string> {
  const result = new Map<string, string>();
  for (let index = 0; index < arguments_.length; index += 2) {
    const key = arguments_[index];
    const value = arguments_[index + 1];
    if (!key?.startsWith("--") || value === undefined || value.startsWith("--") || result.has(key)) {
      throw new SmokeFailure("guard", "INVALID_ARGUMENTS");
    }
    result.set(key, value);
  }
  return result;
}

function requiredArgument(arguments_: Map<string, string>, key: string): string {
  const value = arguments_.get(key);
  if (!value || value.trim() === "") {
    throw new SmokeFailure("guard", "MISSING_ARGUMENT");
  }
  return value;
}

export function ownedPath(root: string, candidate: string, allowRoot = false): string {
  const resolvedRoot = path.resolve(root);
  const resolvedCandidate = path.resolve(candidate);
  const relative = path.relative(resolvedRoot, resolvedCandidate);
  if ((!allowRoot && relative === "") || relative === ".." || relative.startsWith(`..${path.sep}`) || path.isAbsolute(relative)) {
    throw new SmokeFailure("guard", "PATH_OUTSIDE_RUNNER_TEMP");
  }
  return resolvedCandidate;
}

async function requireAbsent(candidate: string, code: string): Promise<void> {
  try {
    await lstat(candidate);
    throw new SmokeFailure("guard", code);
  } catch (error) {
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
      throw error;
    }
  }
}

async function readBounded(stream: ReadableStream<Uint8Array>, child: ReturnType<typeof Bun.spawn>): Promise<string> {
  const reader = stream.getReader();
  const chunks: Uint8Array[] = [];
  let size = 0;
  for (;;) {
    const result = await reader.read();
    if (result.done) {
      break;
    }
    size += result.value.byteLength;
    if (size > maxProcessOutputBytes) {
      terminateProcessTree(child);
      throw new Error("process output exceeded the release-smoke limit");
    }
    chunks.push(result.value);
  }
  const output = new Uint8Array(size);
  let offset = 0;
  for (const chunk of chunks) {
    output.set(chunk, offset);
    offset += chunk.byteLength;
  }
  return new TextDecoder().decode(output);
}

function terminateProcessTree(child: ReturnType<typeof Bun.spawn>): void {
  if (process.platform === "win32") {
    Bun.spawnSync(["taskkill.exe", "/PID", String(child.pid), "/T", "/F"], {
      stdin: "ignore",
      stdout: "ignore",
      stderr: "ignore",
      windowsHide: true,
    });
    return;
  }
  child.kill();
}

async function runCommand(stage: Stage, code: string, command: string[], timeoutMilliseconds: number, environment?: Record<string, string>): Promise<string> {
  const process = Bun.spawn(command, {
    env: { ...Bun.env, ...environment },
    stdin: "ignore",
    stdout: "pipe",
    stderr: "pipe",
    windowsHide: true,
  });
  const stdout = readBounded(process.stdout, process);
  const stderr = readBounded(process.stderr, process);
  let timeout: ReturnType<typeof setTimeout> | undefined;
  const timedOut = new Promise<never>((_, reject) => {
    timeout = setTimeout(() => {
      terminateProcessTree(process);
      reject(new Error("process timed out"));
    }, timeoutMilliseconds);
  });
  try {
    const exitCode = await Promise.race([process.exited, timedOut]);
    const [out] = await Promise.all([stdout, stderr]);
    if (exitCode !== 0) {
      throw new SmokeFailure(stage, code);
    }
    return out.trim();
  } catch (error) {
    if (error instanceof SmokeFailure) {
      throw error;
    }
    throw new SmokeFailure(stage, code);
  } finally {
    if (timeout !== undefined) {
      clearTimeout(timeout);
    }
  }
}

async function loadPackage(root: string, expectedCommit: string): Promise<PackageContext> {
  const receiptPath = await findSingleReceipt(root);
  const receipt = await readAndValidateReceipt(receiptPath, expectedCommit);
  await verifyReceiptFiles(receiptPath, receipt);
  return {
    receiptPath,
    receipt,
    receiptSHA256: await sha256File(receiptPath),
    installerPath: artifactPath(receiptPath, receipt, `talos-agent-${receipt.version}-windows-amd64-setup.exe`),
    probePath: artifactPath(receiptPath, receipt, "talos-release-probe.exe"),
  };
}

async function verifyAuthenticode(package_: PackageContext, expectedSigner: string, expectedCommit: string): Promise<void> {
  const verifier = path.resolve("packaging", "windows", "verify-package.ps1");
  await runCommand(
    "package-verification",
    "PACKAGE_SIGNATURE_INVALID",
    [
      "powershell.exe",
      "-NoProfile",
      "-NonInteractive",
      "-ExecutionPolicy",
      "Bypass",
      "-File",
      verifier,
      "-ReceiptPath",
      package_.receiptPath,
      "-RequireSignature",
      "-ExpectedSignerThumbprint",
      expectedSigner,
      "-ExpectedSourceCommit",
      expectedCommit,
    ],
    120_000,
  );
}

async function verifyInstalledBinaries(stage: Stage, installRoot: string, package_: PackageContext): Promise<void> {
  for (const name of ["talos-desktop.exe", "talos-worker.exe", "talosctl.exe"]) {
    const expected = package_.receipt.artifacts.find((artifact) => artifact.name === name);
    if (!expected) {
      throw new SmokeFailure(stage, "INSTALLED_BINARY_RECEIPT_MISSING");
    }
    const installed = path.join(installRoot, name);
    try {
      const stat = await lstat(installed);
      if (!stat.isFile() || stat.isSymbolicLink() || stat.size !== expected.size || (await sha256File(installed)) !== expected.sha256) {
        throw new SmokeFailure(stage, "INSTALLED_BINARY_MISMATCH");
      }
    } catch (error) {
      if (error instanceof SmokeFailure) {
        throw error;
      }
      throw new SmokeFailure(stage, "INSTALLED_BINARY_MISSING");
    }
  }
}

async function install(stage: Stage, package_: PackageContext, installRoot: string): Promise<void> {
  await runCommand(stage, "INSTALLER_FAILED", [package_.installerPath, "/S"], 300_000);
  await verifyInstalledBinaries(stage, installRoot, package_);
}

async function uninstall(stage: Stage, installRoot: string): Promise<void> {
  const uninstaller = path.join(installRoot, "uninstall.exe");
  await runCommand(stage, "UNINSTALLER_FAILED", [uninstaller, "/S"], 300_000);
  try {
    await lstat(installRoot);
    throw new SmokeFailure(stage, "INSTALL_ROOT_REMAINS");
  } catch (error) {
    if (error instanceof SmokeFailure) {
      throw error;
    }
    if ((error as NodeJS.ErrnoException).code !== "ENOENT") {
      throw new SmokeFailure(stage, "INSTALL_ROOT_CHECK_FAILED");
    }
  }
}

async function runProbe(
  stage: Stage,
  package_: PackageContext,
  action: ProbeReport["action"],
  arguments_: string[],
  expected: Partial<ProbeReport>,
  dataRoot: string,
): Promise<ProbeReport> {
  const output = await runCommand(stage, "RELEASE_PROBE_FAILED", [package_.probePath, action, ...arguments_], 180_000, {
    CI: "true",
    TALOS_RELEASE_PROBE: "1",
    TALOS_RELEASE_PROBE_ROOT: dataRoot,
  });
  try {
    return validateProbeReport(JSON.parse(output) as unknown, {
      action,
      application_version: package_.receipt.version,
      ...expected,
    });
  } catch {
    throw new SmokeFailure(stage, "RELEASE_PROBE_REPORT_INVALID");
  }
}

async function writeEvidence(evidencePath: string, evidence: UpgradeEvidence): Promise<void> {
  await mkdir(path.dirname(evidencePath), { recursive: true });
  await writeFile(evidencePath, `${JSON.stringify(evidence, null, 2)}\n`, { encoding: "utf8", flag: "wx" });
}

async function loadRunnerPreflight(runnerTemp: string, candidate: string, expectedSigner: string): Promise<UpgradeRunnerPreflight> {
  const ownedCandidate = ownedPath(runnerTemp, candidate);
  let stat: Awaited<ReturnType<typeof lstat>>;
  try {
    stat = await lstat(ownedCandidate);
  } catch {
    throw new SmokeFailure("guard", "RUNNER_PREFLIGHT_MISSING");
  }
  if (!stat.isFile() || stat.isSymbolicLink() || stat.size <= 0 || stat.size > 16 * 1024) {
    throw new SmokeFailure("guard", "RUNNER_PREFLIGHT_INVALID");
  }
  const resolved = await realpath(ownedCandidate);
  ownedPath(runnerTemp, resolved);
  try {
    return validateUpgradeRunnerPreflight(JSON.parse(await readFile(resolved, "utf8")) as unknown, expectedSigner);
  } catch {
    throw new SmokeFailure("guard", "RUNNER_PREFLIGHT_INVALID");
  }
}

async function main(): Promise<void> {
  const args = argumentsMap(Bun.argv.slice(2));
  const runnerTemp = process.env.RUNNER_TEMP ?? "";
  const localAppData = process.env.LOCALAPPDATA ?? "";
  const verifierCommit = requiredArgument(args, "--verifier-commit");
  const oldRunID = requiredArgument(args, "--old-run-id");
  const newRunID = requiredArgument(args, "--new-run-id");
  const oldCommit = requiredArgument(args, "--old-source-commit");
  const newCommit = requiredArgument(args, "--new-source-commit");
  const expectedSigner = requiredArgument(args, "--expected-signer-thumbprint").toLowerCase();
  if (
    process.platform !== "win32" ||
    process.env.CI !== "true" ||
    process.env.TALOS_UPGRADE_SMOKE_EPHEMERAL !== "true" ||
    runnerTemp === "" ||
    localAppData === "" ||
    !signerPattern.test(expectedSigner) ||
    !commitPattern.test(oldCommit) ||
    !commitPattern.test(newCommit) ||
    !commitPattern.test(verifierCommit) ||
    !runIDPattern.test(oldRunID) ||
    !runIDPattern.test(newRunID)
  ) {
    throw new SmokeFailure("guard", "UNTRUSTED_RUNNER_OR_INPUT");
  }
  const evidencePath = ownedPath(runnerTemp, requiredArgument(args, "--evidence"));
  const runnerPreflight = await loadRunnerPreflight(runnerTemp, requiredArgument(args, "--runner-preflight"), expectedSigner);
  const evidence: UpgradeEvidence = {
    schema: upgradeEvidenceSchema,
    status: "failed",
    architecture: "amd64",
    signer_thumbprint_sha1: expectedSigner,
    runner_preflight: runnerPreflight,
    verifier_commit: verifierCommit,
    old_run_id: oldRunID,
    new_run_id: newRunID,
    phases: [],
  };
  let currentStage: Stage = "guard";
  let installRoot = "";
  try {
    const oldRoot = await realpath(ownedPath(runnerTemp, requiredArgument(args, "--old-package-root")));
    const newRoot = await realpath(ownedPath(runnerTemp, requiredArgument(args, "--new-package-root")));
    ownedPath(runnerTemp, oldRoot);
    ownedPath(runnerTemp, newRoot);
    installRoot = path.join(localAppData, "Programs", "0disoft", "Talos Agent");
    const dataRoot = path.join(localAppData, "0disoft", "Talos Agent");
    await requireAbsent(installRoot, "PREEXISTING_INSTALLATION");
    await requireAbsent(dataRoot, "PREEXISTING_TALOS_DATA");
    evidence.phases.push({ name: "guard", status: "passed" });

    currentStage = "package-verification";
    const oldPackage = await loadPackage(oldRoot, oldCommit);
    const newPackage = await loadPackage(newRoot, newCommit);
    if (compareVersions(oldPackage.receipt.version, newPackage.receipt.version) >= 0) {
      throw new SmokeFailure(currentStage, "VERSION_ORDER_INVALID");
    }
    await verifyAuthenticode(oldPackage, expectedSigner, oldCommit);
    await verifyAuthenticode(newPackage, expectedSigner, newCommit);
    evidence.old_package = { version: oldPackage.receipt.version, source_commit: oldCommit, receipt_sha256: oldPackage.receiptSHA256 };
    evidence.new_package = { version: newPackage.receipt.version, source_commit: newCommit, receipt_sha256: newPackage.receiptSHA256 };
    evidence.phases.push({ name: currentStage, status: "passed" });

    currentStage = "old-install";
    await install(currentStage, oldPackage, installRoot);
    evidence.phases.push({ name: currentStage, status: "passed", application_version: oldPackage.receipt.version });

    currentStage = "old-vault";
    const created = await runProbe(currentStage, oldPackage, "create", ["--retention-days", "30"], { revision: 1, retention_days: 30 }, dataRoot);
    const backupPath = ownedPath(runnerTemp, path.join(runnerTemp, "talos-upgrade-smoke", "vault-backup.talos-backup"));
    await mkdir(path.dirname(backupPath), { recursive: true });
    const backup = await runProbe(
      currentStage,
      oldPackage,
      "backup",
      ["--vault-id", created.vault_id, "--expected-revision", "1", "--destination", backupPath],
      { vault_id: created.vault_id, revision: 1, retention_days: 30 },
      dataRoot,
    );
    if (!backup.backup_id || !backup.backup_ciphertext_sha256 || !backup.backup_source_schema || !backup.backup_target_schema) {
      throw new SmokeFailure(currentStage, "BACKUP_EVIDENCE_INCOMPLETE");
    }
    evidence.phases.push({ name: currentStage, status: "passed", application_version: oldPackage.receipt.version, revision: 1, retention_days: 30 });

    currentStage = "new-install";
    await install(currentStage, newPackage, installRoot);
    evidence.phases.push({ name: currentStage, status: "passed", application_version: newPackage.receipt.version });

    currentStage = "new-vault";
    await runProbe(
      currentStage,
      newPackage,
      "inspect",
      ["--vault-id", created.vault_id, "--expected-revision", "1", "--expected-retention-days", "30"],
      { vault_id: created.vault_id, revision: 1, retention_days: 30 },
      dataRoot,
    );
    await runProbe(
      currentStage,
      newPackage,
      "update-retention",
      ["--vault-id", created.vault_id, "--expected-revision", "1", "--retention-days", "45"],
      { vault_id: created.vault_id, revision: 2, retention_days: 45 },
      dataRoot,
    );
    evidence.phases.push({ name: currentStage, status: "passed", application_version: newPackage.receipt.version, revision: 2, retention_days: 45 });

    currentStage = "uninstall-retention";
    await uninstall(currentStage, installRoot);
    await runProbe(
      currentStage,
      newPackage,
      "inspect",
      ["--vault-id", created.vault_id, "--expected-revision", "2", "--expected-retention-days", "45"],
      { vault_id: created.vault_id, revision: 2, retention_days: 45 },
      dataRoot,
    );
    evidence.phases.push({ name: currentStage, status: "passed", revision: 2, retention_days: 45 });

    currentStage = "rollback";
    await install(currentStage, oldPackage, installRoot);
    await runProbe(
      currentStage,
      oldPackage,
      "inspect",
      ["--vault-id", created.vault_id, "--expected-revision", "2", "--expected-retention-days", "45"],
      { vault_id: created.vault_id, revision: 2, retention_days: 45 },
      dataRoot,
    );
    await uninstall(currentStage, installRoot);
    evidence.phases.push({ name: currentStage, status: "passed", application_version: oldPackage.receipt.version, revision: 2, retention_days: 45 });

    currentStage = "purge";
    await runProbe(
      currentStage,
      newPackage,
      "purge",
      ["--vault-id", created.vault_id, "--expected-revision", "2", "--confirmation", created.vault_id],
      { vault_id: created.vault_id, revision: 2, retention_days: 0 },
      dataRoot,
    );
    evidence.phases.push({ name: currentStage, status: "passed", revision: 2 });
    evidence.vault = {
      id: created.vault_id,
      final_revision: 2,
      final_retention_days: 45,
      backup_id: backup.backup_id,
      backup_ciphertext_sha256: backup.backup_ciphertext_sha256,
      backup_source_schema: backup.backup_source_schema,
      backup_target_schema: backup.backup_target_schema,
      uninstall_retention_verified: true,
      direct_rollback_read_verified: true,
      purged: true,
    };
    evidence.status = "passed";
  } catch (error) {
    const failure = error instanceof SmokeFailure ? error : new SmokeFailure(currentStage, "UNEXPECTED_FAILURE");
    evidence.failure = { stage: failure.stage, code: failure.code };
    if (installRoot !== "") {
      try {
        const stat = await lstat(path.join(installRoot, "uninstall.exe"));
        if (stat.isFile() && !stat.isSymbolicLink()) {
          await uninstall("cleanup", installRoot);
        } else {
          evidence.failure = { stage: "cleanup", code: "CLEANUP_TARGET_INVALID" };
        }
      } catch (cleanupError) {
        if ((cleanupError as NodeJS.ErrnoException).code !== "ENOENT") {
          evidence.failure = { stage: "cleanup", code: "CLEANUP_FAILED_AFTER_ERROR" };
        }
      }
    }
  }
  await writeEvidence(evidencePath, evidence);
  if (evidence.status !== "passed") {
    throw new SmokeFailure(evidence.failure?.stage ?? currentStage, evidence.failure?.code ?? "UNEXPECTED_FAILURE");
  }
}

if (import.meta.main) {
  try {
    await main();
  } catch (error) {
    if (error instanceof SmokeFailure) {
      console.error(`Windows upgrade smoke failed: ${error.stage}:${error.code}`);
    } else {
      console.error("Windows upgrade smoke failed before bounded evidence could be completed");
    }
    process.exitCode = 1;
  }
}
