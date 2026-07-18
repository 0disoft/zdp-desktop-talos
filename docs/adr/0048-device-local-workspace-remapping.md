# ADR 0048: Device-local workspace remapping

- Status: Accepted
- Date: 2026-07-18

## Context

Task v1 events contain the canonical repository path from the device that created the Task. Replaying those bytes on another device can reconstruct the aggregate, but that path is neither a valid local execution target nor a stable project identity. Treating a remote absolute path as usable would make permission hashes, worktree creation, verification, and memory applicability depend on another machine's filesystem layout.

The Vault enrollment handshake deliberately transfers signing trust and the Vault key, not repository contents. A receiving device therefore needs an explicit local choice and proof that its selected repository contains the immutable Task baseline before Talos can read the Task as executable local work.

## Decision

- Derive one stable `workspace-v1-<sha256>` identifier from the Vault ID and the source workspace hash. Store it on each Task while retaining the source hash for legacy identity checks.
- Add schema 20 `workspace_mappings`. Materialized rows contain only stable identity, source and local-root hashes, verified baseline, active or revoked state, optimistic revision, timestamps, and event references.
- Keep the canonical local root only in encrypted private `workspace.mapping.*` events. Mapping events are device-local control-plane records and never enter the Task, Decision, or Memory export allowlist.
- Bind local Task creation to its current root in the same transaction as the first contract. Imported Tasks receive the stable identity but no local mapping.
- Resolve every Task used by local execution or permission review through the active mapping. Missing or revoked mappings fail with `workspace mapping is required on this device` rather than falling back to the remote path.
- Remapping inspects the selected path through the system-Git adapter, uses its canonical repository root, and verifies the immutable Task baseline with `git cat-file -e <commit>^{commit}` before persisting the mapping.
- Mapping confirmation, remapping, and revocation require optimistic revisions and idempotency keys. A repeated command returns its original encrypted event result; a new semantic confirmation receives its own event instead of attaching two idempotency keys to one event.
- Startup backfills pre-schema-20 local Tasks from their encrypted creation event. Imported Tasks receive only their stable identifier and remain unmapped.

## Consequences

Two devices may use different absolute paths for the same synced Task without weakening baseline or permission checks. Repository work is unavailable until each device proves its own local root. Plaintext mapping state cannot reveal the path, and revocation immediately makes dependent Task reads and execution fail closed.

Task v1 and Memory v1 still carry path-derived sync identity. A later versioned event contract must remove paths from new sync payloads and use the stable workspace identifier for workspace-scoped Memory. Renderer mapping controls, cross-device device revocation, and folder or Git pack exchange remain separate Phase 8 slices.

## Verification

- schema 19 upgrades to schema 20 and validates the Task column and workspace mapping table;
- mapping confirmation, idempotent replay, revocation, and remapping preserve optimistic revisions and stable creation provenance;
- checkpointed SQLite bytes contain neither source nor target path markers;
- imported Task reads fail before mapping and resolve to a distinct target clone after mapping;
- a two-database enrollment scenario verifies the target clone contains the Task baseline, revises from the target path, and replays that revision back to the source path.
