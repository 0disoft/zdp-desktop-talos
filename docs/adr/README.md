# Architecture Decision Records

- Status: Active
- Owner: ZDP/Talos engineering

ADRs record decisions that are expensive to reverse or that establish ownership, trust, storage, protocol, release, or platform boundaries. Product requirements remain in `../product/02-spec.md`; an ADR explains why the architecture satisfies them.

## Index

- `0001-initial-architecture-boundaries.md`: private modular monolith, desktop/worker/CLI split, ZDP identity boundary
- `0002-contract-source-of-truth.md`: ledger, materialized state, schema, projection, and platform-contract authority
- `0003-phase-0-runtime-and-encryption.md`: pinned Phase 0 runtime, IPC, encryption, and production-readiness boundary
- `0004-windows-dpapi-key-store.md`: current-user DPAPI persistence, reference binding, rotation, and unsupported-platform behavior
- `0005-windows-packaging-and-signing.md`: per-user NSIS packaging, current-user certificate signing, receipts, and Vault retention
- `0006-ledger-migrations-and-idempotency.md`: forward SQLite schema versions, request fingerprints, and fail-closed legacy replay
- `0007-atomic-vault-state.md`: transactional revisioned Vault metadata without persisted unlock authority
- `0008-vault-bootstrap-lifecycle.md`: DPAPI and per-Vault database creation, compensation, and in-memory lock cleanup
- `0009-protected-vault-catalog.md`: DPAPI-protected discovery, exact registration compensation, and reopen validation
- `0010-protected-single-instance.md`: per-user encrypted instance IPC and one desktop writer for Vault state
- `0011-recoverable-encrypted-artifact-staging.md`: crash-recoverable encrypted artifact files and schema version 4 metadata
- `0012-restart-safe-vault-hard-purge.md`: protected purge journal, cryptographic erasure ordering, and restart reconciliation
- `0013-read-only-git-workspace-inspection.md`: canonical read-only system Git inspection and bounded repository snapshots
- `0014-atomic-encrypted-task-contracts.md`: atomic Task and first contract persistence with encrypted contract bodies
- `0015-current-snapshot-task-confirmation.md`: clean current-baseline reinspection before desktop contract confirmation
- `0016-optimistic-task-contract-revisions.md`: immutable encrypted revisions with one-winner optimistic concurrency
- `0017-encrypted-decision-answers-and-conflicts.md`: revision-bound encrypted answers that preserve incompatible conflicts
- `0018-vault-scoped-decision-queue-ui.md`: bounded current-baseline Decision review with renderer-state cleanup
- `0019-decision-conflict-resolution-and-question-supersession.md`: explicit answer selection and revision-scoped question replacement
- `0020-owned-task-worktree-lifecycle.md`: baseline-bound detached worktrees with verified ownership and bounded cleanup
- `0021-policy-bound-worker-process-execution.md`: direct argv execution with scoped policy, bounded output, and process-tree cancellation
- `0022-deterministic-process-permission-broker.md`: task-contract-first process authorization with exact-intent scoped grants

Use `0000-template.md` for new decisions. Accepted ADRs are superseded by a new ADR instead of silently rewritten when the decision itself changes. Clarifications that do not alter the decision may be edited with an explicit rationale and validation evidence.
