# Development

- Status: Phase 0 implementation baseline
- Technical owner: ZDP/Talos maintainers

## Current Phase

The repository now proves the Phase 0 executable spine: the renderer builds, desktop/worker/CLI binaries compile, worker IPC is versioned and bounded, and encrypted events survive a SQLite restart without exposing the test plaintext marker. The production OS key-store adapter and signed installer remain explicit release blockers.

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
- `talosctl doctor --json` reports `ready: true` while keeping `production_ready: false` until a production key store exists.
