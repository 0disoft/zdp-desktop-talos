# Talos Agent

- Status: private-alpha engineering baseline
- Visibility: Private
- Repository type: desktop application with a companion CLI

Talos Agent is a local-first coding-agent runtime that remembers durable development decisions and uses them to change future work. It is not a chatbot, a keylogger, a general automation platform, or a ZDP control-plane worker.

## Product Boundary

Talos owns the installed desktop application, local Vault, task execution, Decision Queue, verification evidence, memory compilation, and human-readable projections. ZDP owns shared signup, account identity, consent, membership, and platform audit contracts. Signup links the user identity; it does not upload repositories, prompts, terminal logs, or memories by default.

## Implemented Local Runtime

- Wails `v3.0.0-alpha.2.117` desktop shell, isolated at the root and transport boundary
- Svelte `5.56.4`, Vite `8.1.4`, and TypeScript `6.0.3` renderer
- Go desktop process, versioned worker process, and `talosctl doctor`
- versioned SQLite event store with request-bound transactional idempotency and encrypted payloads
- AES-256-GCM envelope encryption with a random data key per payload
- Windows current-user DPAPI storage for Vault key-encryption keys
- a 4 MiB-bounded, length-prefixed JSON worker protocol over stdio
- a per-user Windows NSIS packaging contract with mandatory release signing and artifact receipts
- protected Vault create, discovery, open, retention, lock, and restart-safe hard purge
- canonical Git workspace inspection, revisioned Task Contracts, Decision Queue, permission review, owned worktrees, verification evidence, and explicit patch apply or discard
- explicit model-egress consent, strict plan proposals, encrypted memory lifecycle, deterministic context evaluation, and public-only projection preview
- signed encrypted sync packs with trusted device membership, DPAPI local signing identity, revocation, restart-safe validation/export journals, explicit event allowlisting, canonical replay, durable conflict quarantine, and fail-closed secret scanning
- fail-closed ZDP account status and offline unlink without exposing account or consent references to the renderer

The current local evidence covers Windows amd64 source builds, schema migrations, DPAPI persistence, rotation, encrypted restart recovery, worker execution, Svelte diagnostics, and the packaging source contract. Production readiness remains false until a provisioned signing host produces and verifies a signed installer and passes native clean-install and N-1 upgrade checks. Shared account link creation remains blocked by upstream ZDP readiness. Enrollment offer/acceptance establishes two-device Vault-key and signing-key trust without copying DPAPI blobs, cancellation and elapsed expiry are durable terminal transitions, verified packs can be replayed by the backend, device-local repository paths can be rebound to stable workspace identities, an authority-signed revocation converges after peer import, and immutable packs can be exchanged through an atomic bounded folder layout. Explicit Git exchange and the renderer workflow are not complete. Plaintext key-file fallback is forbidden.

## Start Here

- Product contract: `docs/product/02-spec.md`
- Architecture: `ARCHITECTURE.md`
- System boundary: `docs/architecture/00-system-boundary.md`
- Security boundary: `docs/security/desktop-security.md`
- Local data: `docs/desktop/local-data.md`
- Windows installer: `docs/desktop/installers.md`
- Alpha readiness: `docs/ops/alpha-readiness.md`
- CLI contract: `docs/cli/command-contract.md`
- Decisions: `docs/adr/`

The repository now contains the local MVP loop from Vault and Task Contract through execution, verification, patch review, memory review, projection, sync-pack validation, local device signing identity, restart-safe export creation, expiry-bounded encrypted enrollment offer/acceptance with cancellation and restart-safe expiry, workspace remapping, path-free Task and Memory sync, ordered canonical replay with conflict quarantine, signed cross-device revocation, and atomic folder exchange. Remaining local product work is explicit Git exchange and the renderer sync workflow. Shared account link creation, funded live-provider smoke, signed installer evidence, native upgrade evidence, hosted CI, and automatic update are external or later promotion gates rather than hidden completion claims.
