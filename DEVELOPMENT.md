# Development

- Status: Architecture phase
- Technical owner: ZDP/Talos maintainers

## Current Phase

The repository is design-only. Phase 0 must prove desktop packaging, worker IPC, OS key-store access, encrypted event round trips, SQLite crash recovery, and frontend bindings before product features are added.

## Planned Layout

```text
cmd/talos-desktop
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

- desktop, worker, and CLI executables build on the selected first platform;
- desktop and worker negotiate a versioned length-prefixed IPC handshake;
- an encrypted event survives restart and no plaintext marker appears in storage;
- domain/application packages remain free of Wails, SQLite-driver, and model-SDK imports;
- selected dependency versions and platform support are recorded in ADRs.
