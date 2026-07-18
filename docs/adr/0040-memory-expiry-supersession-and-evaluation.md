# ADR 0040: Memory expiry, supersession, and deterministic evaluation

- Status: Accepted
- Date: 2026-07-18

## Context

The implemented Memory Gate could approve, stabilize, stale, deprecate, or supersede records internally, but the desktop exposed only candidate approval, rejection, and quarantine. Approved records also had no validity period, so an environment fact or project decision could remain active forever unless a user remembered to revisit it. “Memory quality” had no deterministic measurement contract; counting stored records would reward noise.

## Decision

- Add optional `expires_at` and `superseded_by_memory_id` metadata in SQLite schema 15 and the encrypted memory snapshot. Plaintext materialized state still excludes statements, rationales, applicability terms, and evidence payloads.
- Let the user choose no expiry, 30, 90, or 365 days when approving or reapproving a memory. The renderer sends only the duration; the trusted service owns the timestamp. Expiry never promotes, deletes, or rewrites a memory.
- Exclude expired approved or stable records in the SQLite active query and check expiry again in Context Assembly. This defense-in-depth prevents a stale adapter or alternate reader from injecting an expired record into model context.
- Provide an explicit expiry sweep that transitions due approved or stable records to `stale` with optimistic revision and deterministic idempotency. A query does not silently mutate lifecycle state.
- Expose explicit stable, stale, reapprove, deprecated, and superseded transitions. Automatic stable promotion remains forbidden. Supersession requires a different approved or stable record in the same Vault with the same scope kind and workspace hash. The old record becomes terminal and names its replacement.
- Add a provider-neutral memory evaluation service. Evaluation cases name expected and forbidden memory IDs and report true positives, false positives, false negatives, precision, recall, missing records, and forbidden interventions. The evaluator consumes the same Context Assembly port used by planning; it does not grade model prose.

## Consequences

Active context no longer treats time-limited knowledge as immortal. Users can see and control lifecycle transitions instead of relying on hidden cleanup. Supersession is an auditable relationship rather than a prose convention. Evaluation now rewards relevant selection and penalizes noise or stale intervention, while automatic stable promotion remains deferred until independent-task evidence can be proven.

## Verification

- domain tests cover expiry boundaries and mandatory replacement identity;
- SQLite tests cover schema-15 migration, before/after-expiry active queries, same-scope replacement checks, atomic supersession, and encrypted snapshots;
- Memory Kernel tests cover deterministic expiry sweep behavior;
- Context Assembly tests defensively reject expired adapter output;
- evaluation tests cover precision, recall, missing expected memories, and forbidden stale selection;
- Wails and Svelte checks cover explicit lifecycle controls and server-owned validity durations.
