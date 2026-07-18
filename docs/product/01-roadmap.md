# Roadmap

- Status: Accepted sequence
- Owner: ZDP/Talos product and engineering

| Phase | Scope | Exit evidence |
|---|---|---|
| 0. Architecture spine (active) | repository layout, ADRs, Wails/package spike, worker IPC, encrypted event round trip | storage/IPC/DPAPI and packaging-contract tests pass; provisioned-host signed installer and native upgrade evidence remain |
| 0A. Shared identity linkage (blocked upstream) | ZDP product-link port, device-local Vault membership, encrypted references, explicit unlink | domain/storage lifecycle, fake verifier, dormant S256 HTTP adapter, renderer-safe local status, and offline unlink pass; link creation stays disabled until core live handler, consent/audit transaction, migration apply, product review, and production E2E evidence exist |
| 1. Vault and ledger (complete) | key abstraction, event append, encrypted blobs, materialized state | protected discovery, single-instance writes, create/list/open/lock/retention, recoverable encrypted blobs, and restart-safe hard purge are implemented |
| 2. Workspace and contract (complete) | Git inspection, baseline snapshot, Task and contract revisions | read-only inspection, atomic encrypted persistence, clean current-baseline confirmation, and optimistic immutable revisions are implemented |
| 3. Decision Queue (complete) | scoped blocking, answer revision, conflict handling | encrypted persistence, stale rejection, conflict preservation, explicit resolution, question supersession, and Vault-scoped desktop review are implemented |
| 4. Worker and Git (complete) | task worktree, argv process execution, capabilities, cancellation | owned worktrees, policy-bound execution, deterministic permission evaluation, restart-safe journals, an owned typed worker session, durable permission review, and main-process dispatch are implemented |
| 5. Model runtime (complete) | provider port, egress receipt, structured plan and tool intents | deterministic fixture coverage plus a fixed-host OpenAI Responses adapter, environment credential handle, exact workspace/provider consent, strict Structured Output, review-only proposal UI, and separate permission-bound execution are implemented; a user-authorized funded-account smoke and provider-retention review remain external gates |
| 6. Verification and review (complete) | fresh evidence, bounded patch review, apply/discard | state-bound evidence, bounded redacted diff review, explicit apply/discard, primary-worktree conflict checks, and atomic completion transitions are implemented |
| 7. Memory kernel (complete) | extraction boundary, gate, provenance, context assembly | encrypted lifecycle persistence, same-Vault provenance, Decision extraction, explicit approval and lifecycle review, server-owned expiry, same-scope supersession, defensive active-context filtering, current-Task reasons, and deterministic precision/recall evaluation are implemented; automatic stable promotion remains forbidden until independent-task evidence exists |
| 8. Projection and manual sync (active) | deterministic public-only projection and encrypted immutable packs | projection, signed pack codec, trusted device membership, DPAPI local signing identity, revocation, contiguous validation journal, restart-safe export reservation, secret-scan gate, and encrypted exact-pack storage pass; Vault enrollment transfer, event replay/conflicts, and Git exchange remain |
| 9. Alpha hardening (active) | signing, migration, backup, recovery runbooks | local build, migration, doctor, scaffold, package-source, and readiness-matrix evidence pass; signed installer, hosted CI, clean install, N-1 upgrade, uninstall retention, and rollback evidence remain external gates |

Relay, public protocol extraction, integrations repository, OS sandboxing, and vector retrieval begin only after their preceding local contracts are proven.
