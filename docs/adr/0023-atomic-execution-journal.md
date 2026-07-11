# ADR 0023: Atomic Execution Journal

- Status: Accepted
- Date: 2026-07-11

## Context

Permission approval and worker execution cross a crash boundary. If a one-time grant is consumed separately from attempt creation, a crash can either lose the approval without recording work or leave the approval reusable after work may have started. Treating an interrupted external process as failed is also unsafe because the process may have produced side effects before contact was lost.

## Decision

Vault SQLite schema version 8 owns permission grants, runs, and attempts as materialized execution state backed by encrypted ledger events.

- A persisted grant records scope, capability hash, lifecycle state, expiry, creation event, and latest event.
- Preparing an attempt validates the authoritative encrypted Task record and canonical workspace hash inside the transaction.
- A one-time grant is consumed in the same transaction that appends the preparation event and creates the `dispatch_pending` attempt.
- A deny grant can be persisted as policy evidence but can never authorize attempt preparation.
- One active run is allowed per Vault workspace, and one dispatch-pending attempt is allowed per run. Partial unique indexes enforce both invariants.
- Creation and preparation event IDs are separate from latest event IDs so old idempotency keys still resolve after later transitions.
- Attempt completion is a compare-and-swap transition from `dispatch_pending`. Late completion after reconciliation conflicts instead of overwriting recovery state.
- A run cannot close while it has a dispatch-pending attempt. Closing the run releases the active-workspace uniqueness constraint.
- Restart reconciliation converts pending attempts and their active runs to `unknown` in one transaction and appends encrypted reconciliation events. Unknown means effects may have occurred and reconciliation is required before retry.

Execution events store identifiers, bounded status, hashes, and safe error codes. They do not store raw command output, secrets, or unrestricted process arguments.

## Consequences

The durable journal distinguishes denial, prepared dispatch, known completion, and unknown outcome without guessing after a crash. Idempotent retries return current materialized state associated with the original command event.

Schema migration is forward-only. Existing task workspace hashes are not rewritten because older rows hash the original path spelling; attempt preparation derives the canonical permission workspace hash from the encrypted authoritative Task event instead.

Main-process assembly must still order operations as broker evaluation, atomic attempt preparation, IPC dispatch, attempt completion, and run completion. OS sandboxing and user-facing permission review remain separate work.
