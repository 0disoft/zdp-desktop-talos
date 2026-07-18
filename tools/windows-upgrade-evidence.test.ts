import { describe, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { readAndValidateUpgradeEvidence, validateUpgradeEvidence, type UpgradeEvidenceExpectations } from "./windows-upgrade-contract";
import { argumentsMap, parseExpectations, verificationSummary } from "./windows-upgrade-evidence";

const fixturePath = path.join("contracts", "fixtures", "release", "v1", "valid-windows-upgrade-evidence.json");
const expected: UpgradeEvidenceExpectations = {
  expectedSignerThumbprint: "f".repeat(40),
  verifierCommit: "c".repeat(40),
  oldRunID: "101",
  newRunID: "202",
  oldSourceCommit: "a".repeat(40),
  newSourceCommit: "b".repeat(40),
  requirePassed: true,
};

async function validEvidence(): Promise<Record<string, unknown>> {
  return JSON.parse(await readFile(fixturePath, "utf8")) as Record<string, unknown>;
}

describe("Windows upgrade evidence verifier", () => {
  test("accepts the exact passing artifact and emits a path-free summary", async () => {
    const verified = await readAndValidateUpgradeEvidence(fixturePath, expected);
    expect(verified.evidence.status).toBe("passed");
    expect(verified.sha256).toMatch(/^[a-f0-9]{64}$/);
    const summary = verificationSummary(verified.evidence, verified.sha256);
    expect(summary.old_version).toBe("0.33.0");
    expect(Object.keys(summary)).toEqual([
      "schema",
      "status",
      "architecture",
      "signer_thumbprint_sha1",
      "verifier_commit",
      "old_run_id",
      "new_run_id",
      "old_version",
      "new_version",
      "phase_count",
      "evidence_sha256",
    ]);
    const summaryFixture = JSON.parse(
      await readFile(path.join("contracts", "fixtures", "release", "v1", "valid-windows-upgrade-evidence-verification.json"), "utf8"),
    ) as Record<string, unknown>;
    summaryFixture.evidence_sha256 = verified.sha256;
    expect(summary).toEqual(summaryFixture);
    expect(JSON.stringify(summary)).not.toContain("contracts");
    expect(JSON.stringify(summary)).not.toContain("local_path");
  });

  test("rejects signer drift, rollback version order, and phase reordering", async () => {
    const signerDrift = await validEvidence();
    signerDrift.signer_thumbprint_sha1 = "e".repeat(40);
    expect(() => validateUpgradeEvidence(signerDrift, expected)).toThrow("identity");

    const rollback = await validEvidence();
    (rollback.new_package as Record<string, unknown>).version = "0.32.0";
    expect(() => validateUpgradeEvidence(rollback, expected)).toThrow("strictly increasing");

    const reordered = await validEvidence();
    const phases = reordered.phases as Array<Record<string, unknown>>;
    [phases[2], phases[3]] = [phases[3], phases[2]];
    expect(() => validateUpgradeEvidence(reordered, expected)).toThrow("exact successful prefix");

    const unknownField = await validEvidence();
    unknownField.local_path = "C:\\runner-secret";
    expect(() => validateUpgradeEvidence(unknownField, expected)).toThrow("unsupported field");
  });

  test("accepts bounded failure evidence only when passing status is not required", async () => {
    const failed = await validEvidence();
    failed.status = "failed";
    delete failed.old_package;
    delete failed.new_package;
    delete failed.vault;
    failed.phases = [{ name: "guard", status: "passed" }];
    failed.failure = { stage: "package-verification", code: "PACKAGE_SIGNATURE_INVALID" };
    expect(validateUpgradeEvidence(failed, { ...expected, requirePassed: false }).status).toBe("failed");
    expect(() => validateUpgradeEvidence(failed, expected)).toThrow("failed native run");
  });

  test("parses an exact promotion-verifier command contract", () => {
    const parsed = parseExpectations(
      argumentsMap([
        "--evidence",
        "evidence.json",
        "--expected-signer-thumbprint",
        "f".repeat(40),
        "--verifier-commit",
        "c".repeat(40),
        "--old-run-id",
        "101",
        "--new-run-id",
        "202",
        "--old-source-commit",
        "a".repeat(40),
        "--new-source-commit",
        "b".repeat(40),
        "--require-passed",
        "true",
      ]),
    );
    expect(parsed.requirePassed).toBe(true);
    expect(() => argumentsMap(["--evidence", "one", "--evidence", "two"])).toThrow("invalid or duplicate");
    expect(() => parseExpectations(new Map([...argumentsMap([
      "--evidence",
      "evidence.json",
      "--expected-signer-thumbprint",
      "f".repeat(40),
      "--verifier-commit",
      "c".repeat(40),
      "--old-run-id",
      "101",
      "--new-run-id",
      "202",
      "--old-source-commit",
      "a".repeat(40),
      "--new-source-commit",
      "b".repeat(40),
      "--require-passed",
      "true",
    ]), ["--ignored", "value"]]))).toThrow("unsupported verification argument set");
  });

  test("runs the promotion verifier CLI without exposing the evidence path", () => {
    const result = Bun.spawnSync([
      "bun",
      "tools/windows-upgrade-evidence.ts",
      "--evidence",
      fixturePath,
      "--expected-signer-thumbprint",
      "f".repeat(40),
      "--verifier-commit",
      "c".repeat(40),
      "--old-run-id",
      "101",
      "--new-run-id",
      "202",
      "--old-source-commit",
      "a".repeat(40),
      "--new-source-commit",
      "b".repeat(40),
      "--require-passed",
      "true",
    ]);
    expect(result.exitCode).toBe(0);
    const output = new TextDecoder().decode(result.stdout).trim();
    expect(JSON.parse(output).schema).toBe("talos.windows-upgrade-evidence-verification/1");
    expect(output).not.toContain(fixturePath);
  });
});
