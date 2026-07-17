# Roadmap

- Status: Accepted sequence
- Owner: ZDP/Talos product and engineering

| Phase | Scope | Exit evidence |
|---|---|---|
| 0. Architecture spine (active) | repository layout, ADRs, Wails/package spike, worker IPC, encrypted event round trip | storage/IPC/DPAPI and packaging-contract tests pass; provisioned-host signed installer and native upgrade evidence remain |
| 0A. Shared identity linkage (active) | ZDP product-link port, device-local Vault membership, encrypted references, explicit unlink | domain/storage lifecycle, fake verifier, dormant S256 HTTP adapter, and upstream create·complete·exchange contract are implemented; trusted production endpoint wiring and the user-facing flow remain |
| 1. Vault and ledger (complete) | key abstraction, event append, encrypted blobs, materialized state | protected discovery, single-instance writes, create/list/open/lock/retention, recoverable encrypted blobs, and restart-safe hard purge are implemented |
| 2. Workspace and contract (complete) | Git inspection, baseline snapshot, Task and contract revisions | read-only inspection, atomic encrypted persistence, clean current-baseline confirmation, and optimistic immutable revisions are implemented |
| 3. Decision Queue (complete) | scoped blocking, answer revision, conflict handling | encrypted persistence, stale rejection, conflict preservation, explicit resolution, question supersession, and Vault-scoped desktop review are implemented |
| 4. Worker and Git (complete) | task worktree, argv process execution, capabilities, cancellation | owned worktrees, policy-bound execution, deterministic permission evaluation, restart-safe journals, an owned typed worker session, durable permission review, and main-process dispatch are implemented |
| 5. Model runtime (complete) | provider port, egress receipt, structured plan and tool intents | deterministic fixture provider completes a bounded fake-repo scenario through redaction, encrypted receipt persistence, contract-indexed Tool Intents, and the existing permission/execution boundary; production provider selection and disclosure UI remain separate work |
| 6. Verification and review (complete) | fresh evidence, bounded patch review, apply/discard | state-bound evidence, bounded redacted diff review, explicit apply/discard, primary-worktree conflict checks, and atomic completion transitions are implemented |
| 7. Memory kernel (complete) | extraction boundary, gate, provenance, context assembly | encrypted candidate and lifecycle persistence, same-Vault provenance, explicit review, bounded deterministic assembly, and an approved memory changing a later fixture plan with an applicability reason are implemented; production extraction and review UI remain separate work |
| 8. Projection and manual sync | redacted projection and encrypted immutable packs | duplicate and conflicting two-device fixtures pass |
| 9. Alpha hardening | signing, migration, backup, recovery runbooks | limited private alpha is supportable |

Relay, public protocol extraction, integrations repository, OS sandboxing, and vector retrieval begin only after their preceding local contracts are proven.
