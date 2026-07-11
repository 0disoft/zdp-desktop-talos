# Roadmap

- Status: Accepted sequence
- Owner: ZDP/Talos product and engineering

| Phase | Scope | Exit evidence |
|---|---|---|
| 0. Architecture spine (active) | repository layout, ADRs, Wails/package spike, worker IPC, encrypted event round trip | storage/IPC/DPAPI and packaging-contract tests pass; provisioned-host signed installer and native upgrade evidence remain |
| 1. Vault and ledger (active) | key abstraction, event append, encrypted blobs, materialized state | protected discovery, single-instance writes, create/list/open/lock/retention UI, and recoverable encrypted blob staging are implemented; hard purge remains |
| 2. Workspace and contract | Git inspection, baseline snapshot, Task and contract revisions | task can be contracted without a live model |
| 3. Decision Queue | scoped blocking, answer revision, conflict handling | unrelated steps continue while a decision is open |
| 4. Worker and Git | task worktree, argv process execution, capabilities, cancellation | primary worktree remains untouched |
| 5. Model runtime | provider port, egress receipt, structured plan and tool intents | one provider completes a bounded fake-repo scenario |
| 6. Verification and review | fresh evidence, diff review, apply/discard | stale evidence cannot complete a task |
| 7. Memory kernel | extraction, gate, provenance, context assembly | approved memory changes a later plan and is explainable |
| 8. Projection and manual sync | redacted projection and encrypted immutable packs | duplicate and conflicting two-device fixtures pass |
| 9. Alpha hardening | signing, migration, backup, recovery runbooks | limited private alpha is supportable |

Relay, public protocol extraction, integrations repository, OS sandboxing, and vector retrieval begin only after their preceding local contracts are proven.
