import { describe, expect, test } from "bun:test";
import { compareVersions, releaseProbeSchema, validateProbeReport, validateReceipt } from "./windows-upgrade-contract";

const sourceCommit = "a".repeat(40);

function validReceipt(): Record<string, unknown> {
  const version = "0.34.0";
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
        application_version: "0.34.0",
        vault_id: "vault-1",
        revision: 2,
        retention_days: 0,
      },
      { action: "purge", application_version: "0.34.0", vault_id: "vault-1", revision: 2, retention_days: 0 },
    );
    expect(report.retention_days).toBe(0);
  });

  test("rejects an unknown report field", () => {
    expect(() =>
      validateProbeReport(
        {
          schema: releaseProbeSchema,
          action: "inspect",
          application_version: "0.34.0",
          vault_id: "vault-1",
          revision: 1,
          retention_days: 30,
          local_path: "C:\\secret",
        },
        { action: "inspect", application_version: "0.34.0" },
      ),
    ).toThrow("unsupported field");
  });
});
