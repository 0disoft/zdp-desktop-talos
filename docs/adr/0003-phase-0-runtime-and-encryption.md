# ADR 0003: Phase 0 Runtime and Encryption

- Status: Accepted
- Date: 2026-07-10
- Owners: ZDP/Talos engineering

## Context

Talos needs an executable architecture spine before task orchestration or memory features can be trusted. Wails v3 is still alpha, the renderer toolchain is moving quickly, and local event payloads may contain private source material. A separate process by itself is not an operating-system sandbox, and an encrypted SQLite claim is meaningless if keys fall back to plaintext files.

## Decision

- Pin Go modules and frontend dependencies exactly. Phase 0 uses Go 1.26 language semantics, Wails `v3.0.0-alpha.2.117`, `modernc.org/sqlite v1.53.0`, Svelte `5.56.4`, Vite `8.1.4`, TypeScript `6.0.3`, and `@wailsio/runtime 3.0.0-alpha.97`.
- Keep Wails imports at the executable shell and transport boundary. Domain and application packages depend on ports rather than Wails, SQLite, or a model SDK.
- Use a separate worker with a versioned, length-prefixed JSON protocol over stdio. Frames are limited to 4 MiB, stdout is protocol-only, and heartbeat and shutdown are explicit messages.
- Store event metadata and idempotency state transactionally in SQLite. Encrypt event payloads with a random AES-256-GCM data key per object, then wrap that key with the Vault key-encryption key using authenticated context.
- Reject `secret` payloads before storage. Do not claim the worker is an OS sandbox.
- Report the production key store as unsupported until a platform adapter exists. Never create a plaintext key-file fallback.

## Consequences

The Phase 0 binaries, storage tests, IPC tests, and doctor probe can run on Windows amd64 today. `talosctl doctor` can report local engineering readiness while separately reporting production readiness as false. Wails alpha changes and the currently separate Go/JavaScript Wails version streams remain upgrade risks and require a deliberate compatibility check.

Task execution, Git worktrees, model calls, signup integration, production Vault creation, installer signing, and automatic updates remain out of scope for this decision.

## Evidence

- frontend diagnostics and production build
- complete Go test suite
- desktop, worker, and CLI compilation
- doctor encrypted-SQLite restart and plaintext-marker check
- doctor worker handshake using protocol version 1
