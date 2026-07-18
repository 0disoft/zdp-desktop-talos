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
- `0023-atomic-execution-journal.md`: schema v8 grants, runs, attempts, one-time consumption, and unknown-outcome recovery
- `0024-owned-worker-client-session.md`: typed request correlation, protocol failure handling, bounded process ownership, and reaping
- `0025-idempotent-execution-coordinator.md`: permission-first dispatch, atomic attempt preparation, terminal replay, and unknown-outcome recovery
- `0026-durable-permission-review-requests.md`: encrypted exact-intent review requests, atomic grant resolution, and dispatch-free review persistence
- `0027-bounded-permission-review-ui.md`: server-owned grant expiry, task-bounded approval choices, and use-case-specific Wails review methods
- `0028-structured-task-verification-commands.md`: policy-resolved rule IDs and exact argv in immutable Task Contract revisions
- `0029-contract-derived-verification-execution.md`: current-revision command resolution, bootstrap-owned tool policy, and bounded Wails execution
- `0030-atomic-state-bound-verification-evidence.md`: schema v10 evidence, deterministic worktree state hashes, and evidence-free-success rejection
- `0031-owned-worktree-reopen-for-reverification.md`: marker-verified retained worktrees for repeat verification and restart recovery
- `0032-bounded-patch-review-freshness-gate.md`: read-only changed-file review and fail-closed current-evidence freshness classification
- `0033-redacted-bounded-safe-diff-viewer.md`: hook-free diff capture, deterministic secret redaction, and renderer payload budgets
- `0034-atomic-patch-actions-and-completion-gate.md`: idempotent apply/discard journaling, primary-worktree preconditions, and atomic terminal Task outcomes
- `0035-zdp-account-link-boundary.md`: normalized verified account references, encrypted Vault membership lifecycle, and dormant fake-verifier boundary
- `0036-bounded-model-planning-and-egress-receipts.md`: provider-neutral planning, encrypted metadata-only egress receipts, and contract-indexed Tool Intents
- `0037-gated-memory-lifecycle-and-context-assembly.md`: encrypted candidate lifecycle, provenance gates, bounded applicability, and explainable plan context
- `0038-decision-derived-memory-review-surface.md`: deterministic user-Decision extraction, idempotent candidate replay, explicit review, and current-Task reasons
- `0039-explicit-openai-plan-proposal-boundary.md`: fixed-host Responses adapter, environment credential handle, exact egress consent, and review-only proposals
- `0040-memory-expiry-supersession-and-evaluation.md`: schema-15 validity periods, explicit replacement links, lifecycle UI, and retrieval precision/recall
- `0041-deterministic-public-memory-projection.md`: public-only deterministic Markdown/YAML/JSONL projection, fail-closed scanning, and bounded preview
- `0042-signed-encrypted-immutable-sync-pack.md`: contiguous device sequence, Vault-bound encryption, ciphertext identity, and Ed25519 manifest verification
- `0043-durable-sync-membership-and-validation-journal.md`: trusted device keys, revocation, conditional sequence cursors, and encrypted pack receipts
- `0044-fail-closed-account-status-surface.md`: local-only account status, renderer-safe DTOs, offline unlink, and blocked link creation
- `0045-dpapi-device-identity-and-export-journal.md`: DPAPI signing identity, deterministic device IDs, schema-17 event origins, atomic export reservations, and encrypted ready packs
- `0046-allowlisted-canonical-sync-replay.md`: explicit export allowlist, self-contained Decision v2 events, schema-18 replay outcomes, and conflict quarantine
- `0047-expiry-bounded-vault-enrollment.md`: random-capability offer/acceptance, DPAPI-local target identity, schema-19 lifecycle journal, and two-Vault replay proof
- `0048-device-local-workspace-remapping.md`: stable Vault-bound workspace IDs, schema-20 encrypted local mapping journal, baseline verification, and fail-closed imported Tasks
- `0049-path-free-task-sync-v2.md`: path-free Task v2 events, schema-21 legacy snapshot bridge, strict replay identity, and v1 export retirement

Use `0000-template.md` for new decisions. Accepted ADRs are superseded by a new ADR instead of silently rewritten when the decision itself changes. Clarifications that do not alter the decision may be edited with an explicit rationale and validation evidence.
