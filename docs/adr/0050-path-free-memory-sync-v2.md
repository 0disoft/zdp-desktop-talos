# ADR 0050: Path-free Memory sync v2

- Status: Accepted
- Date: 2026-07-18

## Context

Task sync v2 established a Vault-bound workspace identifier and a device-local path mapping, but workspace-scoped Memory v1 still serialized an absolute `workspace_root`. Encryption hid the value in storage and transit without fixing the identity model: a Memory created in one clone could not apply to the same workspace mapped at a different path, and exporting the v1 event preserved device-specific path data forever.

Rewriting an existing event under the same event ID would invalidate immutable provenance. Redacting only at export would also make the signed bytes disagree with the canonical local event. Existing local Memory history therefore needs a new schema and an explicit provenance bridge.

## Decision

- Memory schema v2 represents workspace scope with `workspace_id` and `source_workspace_hash`. It forbids `workspace_root`. The identifier must equal the Vault-bound identifier derived from the source hash.
- New candidate and lifecycle events use schema v2. Context assembly and active-memory lookup compare the stable workspace identifier, never a device path hash.
- Schema 22 adds `memory_records.workspace_id` and `memory_sync_snapshots`. Startup derives missing materialized identifiers from the existing Vault and source hash before assembling context.
- Each locally originated Memory v1 revision receives one `memory.record.snapshot` v2 event. The bridge records source and snapshot event IDs and does not replace the original event or materialized aggregate pointer.
- Imported v1 events never generate exportable snapshots. Memory v1 remains locally readable but is removed from the sync allowlist.
- Revision-one snapshots become the target aggregate's candidate provenance. Later snapshots refer to that snapshot event as `created_event_id`, so a target never depends on an unexported v1 event ID.
- Replay requires the referenced evidence events, exact aggregate revision order, immutable identity fields, valid lifecycle transitions, matching supersession scope, and monotonic timestamps. Invalid input is quarantined or conflicted without mutating canonical state.
- Device-local repository mapping remains outside Memory payloads. A target may retain imported Memory before mapping, but context assembly applies it only when a local Task resolves to the same workspace identifier.

## Consequences

The same approved workspace Memory can change context assembly on another enrolled device even when the repository clone path differs. New Memory packs contain no absolute workspace path, and existing local v1 history remains transferable without mutating signed source events.

Schema 22 migration and startup reconciliation are required before Memory reads. A corrupt source hash, mismatched workspace identifier, missing candidate snapshot, missing evidence, or imported legacy origin fails closed. Cross-device device revocation propagation, folder or Git pack exchange, and renderer workflow remain separate Phase 8 work.

## Verification

- new candidate and lifecycle payloads use schema v2 and contain no `workspace_root` or local path bytes;
- schema-21 databases migrate to schema 22 and receive stable workspace identifiers;
- each local v1 Memory revision receives one v2 snapshot while imported v1 events remain excluded;
- reopening does not create duplicate bridge events or block on SQLite's single connection;
- sync replay applies an approved workspace Memory at a different clone path and context assembly selects it by `workspace_id`;
- malformed identifiers, source hashes, revision gaps, missing evidence, and incompatible replacements fail closed.
