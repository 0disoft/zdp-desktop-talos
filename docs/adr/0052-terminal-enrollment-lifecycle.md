# ADR 0052: Terminal enrollment cancellation and expiry

- Status: Accepted
- Date: 2026-07-18

## Context

Enrollment offers and recipient acceptances were durable, expiry-bounded, and single-completion on the issuer, but their journal had no terminal state before completion. An offer could be abandoned only by waiting, an accepted response retained its encrypted package indefinitely, and reopening a Vault did not materialize elapsed expiry. A renderer cancel button without an atomic storage transition would merely hide a still-usable capability.

The transfer secret is a bearer capability. Cancellation on one disconnected installation cannot claw back an offer that another offline holder already copied and decrypted. The local system can nevertheless stop issuer completion, erase the recoverable recipient response, and make elapsed expiry durable and auditable.

## Decision

- Extend the enrollment state machine with terminal `canceled` and `expired` states in schema 23.
- Permit cancellation only from issuer `offered` or recipient `accepted` before the recorded expiry. `completed`, `canceled`, and `expired` are terminal.
- Permit expiry only when the recorded deadline has elapsed. The transition is based on the stored deadline, not caller-supplied package data.
- Write the terminal event and materialized row update in one SQLite transaction with a conditional previous-state predicate.
- Clear `acceptance_envelope` on recipient cancellation or expiry. Preserve the non-secret source device ID and package hashes as audit evidence.
- Make equivalent repeated cancellation or expiry idempotent. A different terminal outcome or completion after a terminal transition conflicts.
- Reconcile elapsed active enrollments when the store opens. Candidate IDs are collected and the query cursor is closed before transitions so the single-connection SQLite store cannot deadlock itself.
- Recheck and materialize expiry before issuer completion, then require the stored state to be exactly `offered` before registering the target device. A canceled or expired offer cannot create trust as a completion side effect.
- Return an acceptance package only while a recipient record remains `accepted`; terminal records never decrypt or expose the cleared response.

## Consequences

Local cancellation and elapsed expiry survive restart, block late issuer completion, and reduce retained sensitive transfer material. An already copied offer may still be accepted on a disconnected target because cancellation is not a remote wipe and enrollment control events are not distributed. The issuer will refuse completion after cancellation or expiry, and operators should revoke any target key that was trusted before an incident.

Folder or Git exchange and the renderer must present this limitation plainly. A future relay may shorten delivery latency but cannot retroactively revoke a Vault key already disclosed to an offline holder.

## Validation

- issuer and recipient records transition atomically to canceled;
- repeated equivalent cancellation is idempotent and late completion conflicts;
- recipient cancellation removes the encrypted acceptance response;
- store startup materializes elapsed expiry and a second sweep is empty;
- issuer completion checks terminal state before target device registration;
- schema 15 through 23 migration and the complete Go suite pass.
