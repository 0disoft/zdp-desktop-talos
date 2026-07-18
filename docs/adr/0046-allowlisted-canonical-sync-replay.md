# ADR 0046: Allowlisted canonical sync replay

- Status: Accepted
- Date: 2026-07-18
- Supersedes: the export-scope rule in `0045-dpapi-device-identity-and-export-journal.md`

## Context

ADR 0045 excluded only `sync.*` control events. That rule was too broad: permission grants, worker attempts, model receipts, patch actions, account-link state, Vault lifecycle, and other device-local records could enter an encrypted pack even though another device must not materialize them. A validated pack also remained inert, so duplicate delivery, competing aggregate revisions, and unsupported payloads had no durable outcome.

Decision question events created before this slice also omitted the Task, category, and repository revision needed to reconstruct a Decision on another device. Guessing those fields from unrelated local state would turn replay into last-write-wins corruption.

## Decision

- Export only an explicit `(event_type, schema_version)` allowlist. The first list contains Task Contract v1, self-contained Decision v2, and Memory v1 lifecycle events. Vault, account, permission, execution, model, patch, artifact, projection, and sync-control events remain device-local.
- New Decision events use schema version 2. Question events carry `task_id`, `category`, and `expected_repository_revision`. Existing schema-v1 Decision events remain readable locally but are not exported or guessed during replay.
- Add schema 18 replay journals. A replay batch binds the validated pack, device sequence range, applied/conflicted/quarantined counts, completion time, and an encrypted audit event. Every sequence receives an event hash, stable outcome, and safe reason code.
- Preserve the remote `event_id`, occurrence time, payload, sensitivity, and originating device sequence. Imported events receive an `imported` origin in the same transaction so they cannot be exported again.
- Apply one validated pack in one SQLite transaction. Unexpected storage failures roll back the complete replay. Valid but competing aggregate facts remain in the encrypted ledger and receive `conflicted`; malformed, unsupported, legacy, or dependency-incomplete events receive `quarantined` without mutating materialized state.
- Task revisions require the next optimistic revision and immutable workspace/baseline identity. Memory transitions require existing provenance, immutable content identity, an allowed lifecycle transition, and valid replacement state. Concurrent distinct Decision answers are both preserved and make the materialized Decision `conflicted`; last-write-wins is forbidden.
- Reapplying the same pack and exact event hashes returns the stored result without another event or pointer update. Reusing a pack identity with different event bytes fails closed.

## Consequences

The backend can now turn a verified pack into canonical encrypted events and materialized Task, Decision, and Memory state without recursively exporting imports or leaking device-local receipts. Conflict and quarantine are durable facts rather than log messages.

This does not yet provide Vault-key enrollment, file or Git exchange, renderer controls, workspace-path remapping, or automatic conflict resolution. Those remain explicit later gates.

## Verification

- schema 15 upgrades through schema 18 and validates both replay tables;
- an eight-event fixture applies Task creation/revision, Decision creation/answer, and Memory creation/approval in order;
- a competing Task creation is retained as `conflicted`, and a permission event is retained as `quarantined` without materialization;
- exact replay is idempotent while changed bytes under the same pack identity fail closed;
- imported events are excluded from the next export;
- importer tests prove verified decoded events reach the replay store only after signature and membership validation.
