# ADR 0007: Atomic Revisioned Vault State

- Status: Accepted
- Date: 2026-07-11
- Owners: ZDP/Talos engineering

## Context

The ledger could durably append encrypted events, but it had no implemented current-state table for Vault metadata. Callers therefore could not query retention settings without inventing an in-memory authority or replaying the full event history. Updating an event and a separate state row in different transactions would also permit a crash to leave them disagreeing.

Vault lock and unlock are different: they describe possession of usable key material in the current process. Persisting an `unlocked` flag would falsely restore authority after a crash or sign-out.

## Decision

- Add schema version 3 with a `STRICT` `vault_states` table containing Vault ID, positive revision, lifecycle status, bounded retention days, timestamps, and the last event ID.
- Keep Vault metadata authoritative in the local SQLite database. Create and retention-update commands append their encrypted event, update materialized state, and claim idempotency in one short transaction.
- Require an expected revision for retention changes. A stale revision returns a conflict and leaves the event, state, and idempotency tables unchanged.
- Bind Vault commands to canonical request fingerprints. Exact retries reconstruct their original result from the encrypted event; a changed command using the same key fails closed.
- Keep session lock state out of `vault_states`. Every process start begins locked until the supported OS key store successfully restores the required key material.
- Keep the migration forward-only. Older binaries must reject schema version 3 rather than pretending downgrade compatibility.

## Consequences

Vault metadata reads are bounded and do not require ledger replay. Transaction rollback prevents partial event/state effects, while the revision check prevents lost updates between competing callers. The table does not yet implement hard purge, device membership, listing, or user-facing Vault services; those remain later Phase 1 slices.

The current single-process database owner is the supported write topology. SQLite still has one writer, and cross-process coordination is not implied by optimistic revision checks.

## Evidence

- create, update, exact retry, restart, and encrypted-payload tests;
- stale and concurrent revision conflict tests;
- injected state-write failure rolls back the event and idempotency claim;
- doctor verifies revision 2 survives a close and reopen;
- schema validation rejects missing `vault_states` columns and newer versions.
