# ADR 0059: Signed N-1 evidence uses a real Vault and trusted package ancestry

- Status: Accepted
- Date: 2026-07-19

## Context

The first Windows upgrade smoke installed two signed NSIS packages and preserved a plaintext sentinel beneath the Talos data directory. That proves only that the installer did not recursively delete one path. It does not prove that package receipts identify the executed bytes, that installed binaries match those receipts, that the old release can create an encrypted Vault, that the candidate can open and mutate it, or that the old release can read the post-upgrade state.

The signing and upgrade workflows are separate trust zones. A manually supplied workflow run ID is not package provenance by itself, and a current verifier checkout must not accept a successful artifact from an unrelated branch or workflow.

## Decision

- Publish `talos-release-probe.exe` as a signed package artifact and include it in `talos.windows-package-receipt/1`. Never install the probe on user machines.
- Restrict the probe to an explicitly enabled Windows CI account and the exact current-user Talos data root. It exposes only create, inspect, retention update, encrypted backup, journaled restore, and exact-identity purge operations. Invalid command input is rejected before the Vault runtime opens.
- Resolve both package run IDs through the GitHub Actions API. Require successful manual `main` executions of `.github/workflows/windows-signing.yml`, require the candidate commit to be ahead of the old commit, and require the verifier commit to equal or descend from the candidate commit.
- Parse package receipts with a closed field set. Require exactly the desktop, worker, CLI, release probe, and versioned installer artifacts; exact source commits; signature-required state; release-probe schema; sizes; and SHA-256 hashes. Run the Windows Authenticode verifier against every receipt artifact and the configured public certificate thumbprint.
- Run upgrade orchestration in Bun TypeScript on a distinct disposable `talos-upgrade-smoke` runner. Before package lookup or download, require a fail-closed PowerShell preflight that validates the exact runner contract, pinned Bun version, WebView2, a clean Talos profile, and absence of every current-user code-signing private key. Reject pre-existing Talos installation or data roots, package or evidence paths outside `RUNNER_TEMP`, missing signer identity, and non-ephemeral execution.
- Install the old package and verify installed binary bytes against its receipt. Use the old probe to create and preflight-backup a real encrypted Vault. Install the candidate, verify its installed bytes, open the old Vault, and commit an optimistic retention revision.
- Uninstall the candidate and prove the Vault remains readable. Reinstall the old package and require its old probe to read the candidate-mutated Vault. A direct rollback-read failure blocks promotion; current-binary restore evidence is not a substitute.
- Uninstall the old package, purge the test Vault through the candidate probe, and emit a bounded path-free `talos.windows-upgrade-evidence/1` artifact. Embed the validated preflight facts and bind both preflight and package verification to the same public signer thumbprint. Failure evidence contains only a stage and stable code, never local paths, process output, tokens, or signing material.

## Consequences

The first signed release that contains the release probe becomes the oldest possible baseline for this evidence model. The probe cannot retroactively prove an older package that did not publish it. Until two suitable signed package runs execute successfully on the owned runner, native N-1 status remains blocked.

A passing smoke proves the selected old-to-candidate path, uninstall retention, direct old-binary read compatibility for the exercised Vault revision, and that the inspected current-user and local-machine personal certificate stores had no code-signing private key before package access. It does not prove that the VM was destroyed afterward, authorize unattended updates, prove every skipped-version path, provision the production manifest key, or replace backup-based forward recovery.

## Validation

- strict receipt artifact-set, semantic-version, source-commit, size, hash, and signature metadata tests;
- trusted signing-run workflow, branch, conclusion, and commit-ancestry tests;
- release-probe command parsing before durable runtime access;
- runner-temp containment and duplicate-argument tests;
- upgrade-runner contract, PowerShell parse, private-key absence, WebView2, clean-profile, and signer-binding source-contract tests;
- Windows workflow source-contract tests for key separation, pinned actions, bounded evidence upload, and legacy sentinel removal;
- native signed old/candidate installation evidence remains a provisioned-runner gate.
