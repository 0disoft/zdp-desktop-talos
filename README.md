# Talos Agent

- Status: Phase 0 implementation baseline
- Visibility: Private
- Repository type: desktop application with a companion CLI

Talos Agent is a local-first coding-agent runtime that remembers durable development decisions and uses them to change future work. It is not a chatbot, a keylogger, a general automation platform, or a ZDP control-plane worker.

## Product Boundary

Talos owns the installed desktop application, local Vault, task execution, Decision Queue, verification evidence, memory compilation, and human-readable projections. ZDP owns shared signup, account identity, consent, membership, and platform audit contracts. Signup links the user identity; it does not upload repositories, prompts, terminal logs, or memories by default.

## Implemented Phase 0 Runtime

- Wails `v3.0.0-alpha.2.117` desktop shell, isolated at the root and transport boundary
- Svelte `5.56.4`, Vite `8.1.4`, and TypeScript `6.0.3` renderer
- Go desktop process, versioned worker process, and `talosctl doctor`
- SQLite event store with transactional idempotency and encrypted payloads
- AES-256-GCM envelope encryption with a random data key per payload
- Windows current-user DPAPI storage for Vault key-encryption keys
- a 4 MiB-bounded, length-prefixed JSON worker protocol over stdio

The current build and doctor evidence covers Windows amd64, including DPAPI persistence, rotation, reference binding, and plaintext-marker checks. Production readiness remains false until signed packaging and native platform release checks exist. Plaintext key-file fallback is forbidden.

## Start Here

- Product contract: `docs/product/02-spec.md`
- Architecture: `ARCHITECTURE.md`
- System boundary: `docs/architecture/00-system-boundary.md`
- Security boundary: `docs/security/desktop-security.md`
- Local data: `docs/desktop/local-data.md`
- CLI contract: `docs/cli/command-contract.md`
- Decisions: `docs/adr/`

The repository contains the Phase 0 architecture spine and executable probes. Vault UI workflows, task execution, model providers, Git worktrees, memory compilation, signup integration, and sync remain later phases.
