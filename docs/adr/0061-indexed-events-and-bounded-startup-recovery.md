# ADR 0061: Indexed Events and Bounded Startup Recovery

- Status: Accepted
- Date: 2026-08-09

## Context

Vault event history grows with every task, decision, verification, memory, and sync action. Ordered Vault export and legacy portability queries previously had no matching event indexes. Startup also reran four compatibility reconcilers after every open even though current writers no longer create the legacy Task and Memory forms those reconcilers repair.

Enrollment expiry and staged-artifact recovery are different: time can pass and a crash can leave new staged work after any successful open, so those checks cannot use a permanent completion marker.

## Decision

- Schema 24 adds ordered indexes for `(vault_id, occurred_at, event_id)` and `(schema_version, event_type, occurred_at, event_id)`.
- Schema 24 adds a `recovery_markers` table keyed by a versioned recovery identity. The stored
  completion version belongs to the recovery algorithm, not the enclosing SQLite schema, so later
  unrelated schema upgrades do not invalidate a completed recovery. Existing schema-coupled value
  `24` is accepted once and normalized to recovery version `1`.
- Workspace mapping, legacy Task snapshot, Memory workspace identity, and legacy Memory snapshot reconciliation run in dependency order only when `legacy-portable-state/v24` is absent.
- Record the marker only after every compatibility step succeeds. A crash or failure before that insert leaves the recovery retryable and idempotent.
- Treat a marker with a mismatched schema version as corruption and fail closed.
- Continue enrollment-expiry and staged-artifact reconciliation on every open.
- Restrict Memory workspace-ID backfill to rows whose portable identifier is still empty.

## Consequences

Steady-state Vault open performs one primary-key marker lookup instead of rescanning legacy Task, Memory, and event history. Existing schema 0 through 23 databases migrate forward and run compatibility recovery once. Restoring an older database also lacks the marker and therefore runs the same recovery.

The migration adds index write and storage overhead. It does not add a read pool, relax the single-connection transaction model, or claim that every event query is fully covered.

## Verification

- migration tests require schema 24, both event indexes, and the recovery table;
- query-plan tests require the ordered Vault and schema/type reads to select the intended indexes;
- restart tests require the completion timestamp to remain unchanged after a second open;
- a mismatched marker must fail open;
- schema-20 Task and schema-21 Memory fixtures continue proving the one-time compatibility bridge.
