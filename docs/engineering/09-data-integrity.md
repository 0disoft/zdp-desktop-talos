# Data Integrity

- Status: Accepted baseline

## Event and State Atomicity

Appending an event, advancing stream/device heads, recording idempotency, and updating materialized state occur in one SQLite transaction. Large payload blobs are committed through a recoverable staging protocol so a database row never silently points to absent or unverified ciphertext.

## Integrity Metadata

Events use UUIDv7 identifiers, device-local monotonic sequence numbers, correlation and causation identifiers, schema versions, sensitivity, previous-device hash, event hash, and device signature. Hash chains detect drift; they are not advertised as an immutable external audit ledger against a stolen device key.

## Concurrency and Recovery

Commands carry idempotency keys. Revision-sensitive changes use optimistic concurrency. Decision answers bind to question and repository revisions. Sync consumers assume at-least-once delivery, deduplicate events, reject invalid sequence/signature/schema data, and surface incompatible answers as conflicts.

## Evidence Integrity

Verification evidence is valid only for its recorded repository revision and diff hash. Any patch mutation invalidates affected evidence before task completion. Projections and indexes can be deleted and regenerated from accepted ledger state.
