import { describe, expect, test } from "bun:test";
import { readFile } from "node:fs/promises";
import path from "node:path";
import { compareVersions, releaseProbeSchema, validateProbeReport, validateReceipt, validateUpgradeRunnerPreflight } from "./windows-upgrade-contract";

const sourceCommit = "a".repeat(40);

function validReceipt(): Record<string, unknown> {
  const version = "0.37.0";
  return {
    schema: "talos.windows-package-receipt/1",
    version,
    architecture: "amd64",
    source_commit: sourceCommit,
    signature_required: true,
    release_probe_schema: releaseProbeSchema,
    artifacts: [
      "talos-desktop.exe",
      "talos-worker.exe",
      "talosctl.exe",
      "talos-release-probe.exe",
      `talos-agent-${version}-windows-amd64-setup.exe`,
    ].map((name) => ({ name, sha256: "b".repeat(64), size: 123, signature_status: "Valid", signer_subject: "CN=Talos Test" })),
  };
}

describe("Windows upgrade package contract", () => {
  test("accepts the exact signed five-artifact receipt", () => {
    expect(validateReceipt(validReceipt(), sourceCommit).artifacts).toHaveLength(5);
  });

  test("keeps release receipt fixtures aligned with the runtime parser", async () => {
    const root = path.join("contracts", "fixtures", "release", "v1");
    const valid = JSON.parse(await readFile(path.join(root, "valid-windows-package-receipt.json"), "utf8")) as unknown;
    expect(validateReceipt(valid, sourceCommit).version).toBe("0.37.0");
    const invalid = JSON.parse(await readFile(path.join(root, "invalid-windows-package-receipt-duplicate-artifact.json"), "utf8")) as unknown;
    expect(() => validateReceipt(invalid, sourceCommit)).toThrow("unexpected or duplicate artifact name");
  });

  test("rejects unknown receipt fields", () => {
    expect(() => validateReceipt({ ...validReceipt(), ignored: true }, sourceCommit)).toThrow("unsupported field set");
  });

  test("rejects duplicate artifact identities", () => {
    const receipt = validReceipt();
    const artifacts = receipt.artifacts as Array<Record<string, unknown>>;
    artifacts[4] = { ...artifacts[0] };
    expect(() => validateReceipt(receipt, sourceCommit)).toThrow("unexpected or duplicate artifact name");
  });

  test("rejects an untrusted source commit", () => {
    expect(() => validateReceipt(validReceipt(), "c".repeat(40))).toThrow("source commit");
  });

  test("orders numeric semantic versions without lexical mistakes", () => {
    expect(compareVersions("0.9.9", "0.10.0")).toBeLessThan(0);
    expect(compareVersions("1.0.0", "0.99.99")).toBeGreaterThan(0);
    expect(compareVersions("2.3.4", "2.3.4")).toBe(0);
  });
});

describe("release probe report contract", () => {
  test("accepts explicit zero retention for purge evidence", () => {
    const report = validateProbeReport(
      {
        schema: releaseProbeSchema,
        action: "purge",
        application_version: "0.37.0",
        vault_id: "vault-1",
        revision: 2,
        retention_days: 0,
      },
      { action: "purge", application_version: "0.37.0", vault_id: "vault-1", revision: 2, retention_days: 0 },
    );
    expect(report.retention_days).toBe(0);
  });

  test("rejects an unknown report field", () => {
    expect(() =>
      validateProbeReport(
        {
          schema: releaseProbeSchema,
          action: "inspect",
          application_version: "0.37.0",
          vault_id: "vault-1",
          revision: 1,
          retention_days: 30,
          local_path: "C:\\secret",
        },
        { action: "inspect", application_version: "0.37.0" },
      ),
    ).toThrow("unsupported field");
  });
});

describe("upgrade runner preflight contract", () => {
  const signer = "f".repeat(40);
  const valid = {
    schema: "talos.windows-upgrade-runner-preflight/1",
    architecture: "amd64",
    bun_version: "1.3.14",
    signer_thumbprint_sha1: signer,
    webview2_ready: true,
    signing_private_keys_absent: true,
    installation_absent: true,
    data_absent: true,
  };

  test("binds clean-runner evidence to the expected public signer", () => {
    expect(validateUpgradeRunnerPreflight(valid, signer).signer_thumbprint_sha1).toBe(signer);
    expect(() => validateUpgradeRunnerPreflight(valid, "e".repeat(40))).toThrow("does not satisfy");
  });

  test("keeps preflight fixtures aligned with the runtime parser", async () => {
    const root = path.join("contracts", "fixtures", "release", "v1");
    const validFixture = JSON.parse(await readFile(path.join(root, "valid-windows-upgrade-runner-preflight.json"), "utf8")) as unknown;
    expect(validateUpgradeRunnerPreflight(validFixture, signer).bun_version).toBe("1.3.14");
    const invalidFixture = JSON.parse(
      await readFile(path.join(root, "invalid-windows-upgrade-runner-preflight-private-key.json"), "utf8"),
    ) as unknown;
    expect(() => validateUpgradeRunnerPreflight(invalidFixture, signer)).toThrow("does not satisfy");
  });

  test("rejects private-key or dirty-profile claims", () => {
    expect(() => validateUpgradeRunnerPreflight({ ...valid, signing_private_keys_absent: false }, signer)).toThrow("does not satisfy");
    expect(() => validateUpgradeRunnerPreflight({ ...valid, data_absent: false }, signer)).toThrow("does not satisfy");
    expect(() => validateUpgradeRunnerPreflight({ ...valid, bun_version: "latest" }, signer)).toThrow("does not satisfy");
    expect(() => validateUpgradeRunnerPreflight({ ...valid, certificate_subject: "CN=private" }, signer)).toThrow("unsupported field set");
  });
});
