import { writeFile } from "node:fs/promises";
import { readAndValidateUpgradeEvidence, type UpgradeEvidence, type UpgradeEvidenceExpectations } from "./windows-upgrade-contract";

const commitPattern = /^[a-f0-9]{40}$/;
const runIDPattern = /^[1-9]\d*$/;
const signerPattern = /^[A-Fa-f0-9]{40}$/;
const sha256Pattern = /^[a-f0-9]{64}$/;
const expectedArgumentKeys = new Set([
  "--evidence",
  "--expected-signer-thumbprint",
  "--verifier-commit",
  "--old-run-id",
  "--new-run-id",
  "--old-source-commit",
  "--new-source-commit",
  "--require-passed",
  "--output",
]);
const requiredArgumentKeys = [...expectedArgumentKeys].filter((key) => key !== "--output");

export function argumentsMap(arguments_: string[]): Map<string, string> {
  const result = new Map<string, string>();
  for (let index = 0; index < arguments_.length; index += 2) {
    const key = arguments_[index];
    const value = arguments_[index + 1];
    if (!key?.startsWith("--") || value === undefined || value.startsWith("--") || result.has(key)) {
      throw new Error("invalid or duplicate command arguments");
    }
    result.set(key, value);
  }
  return result;
}

function required(arguments_: Map<string, string>, key: string): string {
  const value = arguments_.get(key);
  if (!value || value.trim() === "") {
    throw new Error("required verification argument is missing");
  }
  return value;
}

export function parseExpectations(arguments_: Map<string, string>): UpgradeEvidenceExpectations & { evidencePath: string; outputPath?: string } {
  if ([...arguments_.keys()].some((key) => !expectedArgumentKeys.has(key)) || requiredArgumentKeys.some((key) => !arguments_.has(key))) {
    throw new Error("unsupported verification argument set");
  }
  const requirePassedValue = required(arguments_, "--require-passed");
  if (requirePassedValue !== "true" && requirePassedValue !== "false") {
    throw new Error("require-passed must be true or false");
  }
  const result = {
    evidencePath: required(arguments_, "--evidence"),
    expectedSignerThumbprint: required(arguments_, "--expected-signer-thumbprint"),
    verifierCommit: required(arguments_, "--verifier-commit"),
    oldRunID: required(arguments_, "--old-run-id"),
    newRunID: required(arguments_, "--new-run-id"),
    oldSourceCommit: required(arguments_, "--old-source-commit"),
    newSourceCommit: required(arguments_, "--new-source-commit"),
    requirePassed: requirePassedValue === "true",
  };
  if (
    !signerPattern.test(result.expectedSignerThumbprint) ||
    !commitPattern.test(result.verifierCommit) ||
    !commitPattern.test(result.oldSourceCommit) ||
    !commitPattern.test(result.newSourceCommit) ||
    !runIDPattern.test(result.oldRunID) ||
    !runIDPattern.test(result.newRunID)
  ) {
    throw new Error("verification identity argument is invalid");
  }
  const outputPath = arguments_.get("--output");
  return outputPath === undefined ? result : { ...result, outputPath: required(arguments_, "--output") };
}

export function verificationSummary(evidence: UpgradeEvidence, sha256: string): Record<string, unknown> {
  if (!sha256Pattern.test(sha256)) {
    throw new Error("evidence hash is invalid");
  }
  return {
    schema: "talos.windows-upgrade-evidence-verification/1",
    status: evidence.status,
    architecture: evidence.architecture,
    signer_thumbprint_sha1: evidence.signer_thumbprint_sha1,
    verifier_commit: evidence.verifier_commit,
    old_run_id: evidence.old_run_id,
    new_run_id: evidence.new_run_id,
    old_version: evidence.old_package?.version ?? null,
    new_version: evidence.new_package?.version ?? null,
    phase_count: evidence.phases.length,
    evidence_sha256: sha256,
  };
}

async function main(): Promise<void> {
  const { evidencePath, outputPath, ...expected } = parseExpectations(argumentsMap(Bun.argv.slice(2)));
  const verified = await readAndValidateUpgradeEvidence(evidencePath, expected);
  const encoded = `${JSON.stringify(verificationSummary(verified.evidence, verified.sha256))}\n`;
  if (outputPath !== undefined) {
    await writeFile(outputPath, encoded, { encoding: "utf8", flag: "wx" });
  }
  console.log(encoded.trimEnd());
}

if (import.meta.main) {
  try {
    await main();
  } catch {
    console.error("Windows upgrade evidence verification failed");
    process.exitCode = 1;
  }
}
