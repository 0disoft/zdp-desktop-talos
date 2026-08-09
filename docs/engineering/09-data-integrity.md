# Data Integrity

- Status: Accepted baseline

## Event and State Atomicity

Appending an event, advancing stream/device heads, recording idempotency, and updating materialized state occur in one SQLite transaction. Large payload blobs are committed through a recoverable staging protocol so a database row never silently points to absent or unverified ciphertext.

Implemented artifact storage writes exclusive same-directory staging files, records `staged` metadata in schema version 4, promotes ciphertext by rename, and then marks metadata `ready`. Vault startup reconciles interrupted promotions, removes abandoned staged rows and narrowly identified orphan stage files, and fails closed when a ready artifact's ciphertext or plaintext integrity metadata does not match.

Implemented Vault metadata uses a `STRICT` materialized table. Vault creation and retention changes append an encrypted event, update the revisioned state row, and claim idempotency atomically. Stale revisions and changed-intent key reuse fail without partial writes. Session lock state is deliberately not persisted: a restarted process must reacquire key authority from the supported OS key store.

Task creation, immutable contract revision 1, its encrypted event, and idempotency claim share one transaction. The Task and revision pointer repeat the baseline under a composite foreign key, while private contract body fields remain encrypted in the event instead of being copied into materialized rows.

Later Task Contract revisions append immutable encrypted events and advance the Task head only when `expected_revision` still matches. Historical idempotency replay is reconstructed from its own revision event, not from the current Task head, and generated occurrence time is excluded from caller-intent hashes.

Decision answers must match both the question revision and repository revision. Equivalent answers converge without a new event; incompatible answers remain separate immutable events and transition the Decision to `conflicted` instead of using last-write-wins.

Account link, unlink, and relink append encrypted private events and advance one Vault-scoped local membership only when `expected_revision` matches. The encrypted event carries the opaque link receipt, account, optional workspace, and consent references; the materialized row carries none of them. The same create idempotency key and correlation may recover a lost exchange response; another correlation cannot consume that command. Unlink clears current active references but leaves encrypted history until hard purge.

SQLite schema changes use monotonic `PRAGMA user_version` migrations. Each migration commits atomically, newer application-incompatible schemas are rejected, and the adapter validates required columns after migration instead of trusting the version integer alone. Schema 24 adds ordered event indexes and a primary-keyed completion marker for legacy portability recovery; the marker is written only after every idempotent compatibility step succeeds, while expiry and staged-artifact recovery remain per-open checks. Migrations are forward-only; backup and roll-forward policy are separate release gates for destructive changes.

Schema 19 journals enrollment offers, accepted responses, and completed issuer handshakes. Offer and acceptance hashes bind exact transfer bytes; the recipient stores the exact acceptance under the Vault envelope so a restart returns the same response instead of minting another target identity. Schema 23 adds terminal cancellation and expiry with conditional prior-state updates. Terminal recipient transitions erase the acceptance envelope, elapsed deadlines reconcile after restart, and issuer completion checks the active state before registering a target. Conflicting bytes, target identities, or terminal outcomes do not overwrite the first accepted transition.

## Integrity Metadata

Events use UUIDv7 identifiers, device-local monotonic sequence numbers, correlation and causation identifiers, schema versions, sensitivity, previous-device hash, event hash, and device signature. Hash chains detect drift; they are not advertised as an immutable external audit ledger against a stolen device key.

## Concurrency and Recovery

Commands carry idempotency keys and canonical request fingerprints. A key returns a prior result only when the fingerprint matches; reuse with different intent is a conflict, and legacy rows without a fingerprint require reconciliation instead of an assumed match. Revision-sensitive changes use optimistic concurrency. Decision answers bind to question and repository revisions. Sync consumers assume at-least-once delivery, deduplicate events, reject invalid sequence/signature/schema data, and surface incompatible answers as conflicts.

## Evidence Integrity

Verification evidence is valid only for its recorded repository revision and diff hash. Any patch mutation invalidates affected evidence before task completion. Projections and indexes can be deleted and regenerated from accepted ledger state.
