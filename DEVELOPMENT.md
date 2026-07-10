# Development

- Status: Phase 0 implementation baseline
- Technical owner: ZDP/Talos maintainers

## Current Phase

The repository now proves the Phase 0 executable spine: the renderer builds, desktop/worker/CLI binaries compile, worker IPC is versioned and bounded, encrypted events survive a SQLite restart, Windows current-user DPAPI protects Vault key-encryption keys, and the Windows NSIS/signing source contract is tested. A provisioned signing-host run and native clean-install/N-1-upgrade evidence remain explicit release blockers.

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

## Definition of Phase 0 Done

- desktop, worker, and CLI executables build on Windows amd64;
- desktop and worker negotiate a versioned length-prefixed IPC handshake;
- an encrypted event survives restart and no plaintext marker appears in storage;
- domain/application packages remain free of Wails, SQLite-driver, and model-SDK imports;
- selected dependency versions and current platform evidence are recorded in ADRs;
- `talosctl doctor --json` proves Windows DPAPI persistence and reports remaining production blockers explicitly.
- Windows packaging scripts parse and their per-user, signing, companion-binary, prerequisite, and Vault-retention contracts pass static tests.

Phase 0 is not release-ready until a provisioned Windows host produces signed artifacts, verifies the package receipt, and passes clean-install and N-1 upgrade smoke checks.
