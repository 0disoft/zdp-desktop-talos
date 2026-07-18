# Development

- Status: private-alpha engineering baseline
- Technical owner: ZDP/Talos maintainers

## Current Phase

The repository now proves the local MVP loop through schema 22: the renderer builds, desktop/worker/CLI binaries compile, Vault and encrypted state survive restart, Task/Decision/permission/execution/patch/memory flows are revisioned and evidence-bound, model egress is explicit, signed sync packs have durable export/import/replay journals, encrypted enrollment offer/acceptance establishes two-device Vault and signing trust, workspace identities survive path changes, and authority-bound device revocation propagates through imported packs. A provisioned signing-host run, native clean-install/N-1-upgrade evidence, upstream product-link promotion, enrollment cancellation/expiry, renderer sync controls, and explicit folder or Git exchange remain blockers rather than implied functionality.

## Current Layout

```text
main.go
cmd/talos-worker
cmd/talosctl
frontend/
internal/domain
internal/application
internal/ports
internal/adapters
internal/transport
internal/security
contracts/
migrations/
packaging/windows/
docs/
```

## Change Rules

- Read `AGENTS.md`, `VALIDATION.md`, `CHECKLIST.md`, and the routed skill first.
- Keep framework and provider code at adapters and transports.
- Add a failing test for every domain invariant or recovered defect.
- Never add a generic shell-command or arbitrary-path desktop binding.
- Dependency installation, network egress, Git remote writes, and schema migrations require explicit policy and evidence.
- Do not claim a validation passed while the generated Taskfile command is still intentionally unconfigured.

## Current engineering baseline

- desktop, worker, and CLI executables build on Windows amd64;
- desktop and worker negotiate a versioned length-prefixed IPC handshake;
- an encrypted event survives restart and no plaintext marker appears in storage;
- domain/application packages remain free of Wails, SQLite-driver, and model-SDK imports;
- selected dependency versions and current platform evidence are recorded in ADRs;
- `talosctl doctor --json` proves Windows DPAPI persistence and reports remaining production blockers explicitly.
- Windows packaging scripts parse and their per-user, signing, companion-binary, prerequisite, and Vault-retention contracts pass static tests.
- the full local coding loop preserves Task scope, permission decisions, verification freshness, patch review, and memory provenance;
- account status is fail-closed and cannot enable link creation while upstream readiness is blocked;
- sync validation rejects untrusted, revoked, duplicate-conflicting, gapped, or tampered packs without claiming remote event application.
- sync export reuses one DPAPI signing identity, reserves contiguous event ranges atomically, blocks likely secrets, and restores exact ready pack bytes after restart.

Private alpha distribution is not ready until a provisioned Windows host produces signed artifacts, verifies the package receipt, and passes clean-install and N-1 upgrade smoke checks. See `docs/ops/alpha-readiness.md` for the evidence matrix and rollback boundary.
