# ADR 0035: ZDP Account Link Boundary

- Status: Accepted
- Date: 2026-07-15

## Context

Talos participates in ZDP shared signup, but the desktop must not become another owner of credentials, sessions, contact methods, integrated profile data, platform membership, or consent truth. Account linkage also must not imply repository, prompt, model response, diff, memory, or Vault synchronization.

The live ZDP authentication callback contract is not yet stable enough to activate in the desktop. A testable local boundary is still required before UI and network code arrive, otherwise provider session types and account policy will leak into Talos domain and storage.

## Decision

- ZDP core remains authoritative for account authentication, session validity, platform membership, consent records, and account audit.
- Talos receives only a normalized verified assertion containing an opaque `subject_ref`, optional `workspace_ref`, `consent_receipt_ref`, and verification time.
- Passwords, session cookies, access or refresh tokens, login IDs, email addresses, phone numbers, nicknames, profile fields, and raw consent documents never cross the inward port.
- The verifier consumes a one-time account-link challenge outside the Talos core. Repeating the same challenge and correlation ID must return the same verified assertion; reuse under a different correlation ID fails closed.
- Talos persists one device-local membership per Vault. Link, unlink, and relink use optimistic revisions and idempotency keys. Relink preserves the local membership identity while replacing the active external references.
- Verified references live only in encrypted private event payloads. The materialized `account_links` table stores Vault ID, local membership ID, lifecycle state, revision, timestamps, and event pointer; it stores no raw or hashed account, workspace, contact, session, or consent reference.
- Unlink appends an encrypted tombstone-style event and clears active references from the current record. Historical encrypted events remain until Vault hard purge; unlink is not advertised as physical deletion.
- ZDP verification failure or network unavailability cannot lock an already-open local Vault or block local review and execution that do not require account services.
- A deterministic fake verifier lives under test support only. Production bootstrap and Wails services do not import or activate it. Live browser/callback transport and user-facing account controls require a later adapter and security review.

## Consequences

The account provider and callback protocol can change without changing the Talos membership model. The first slice is intentionally dormant in production: it proves domain, storage, privacy, concurrency, and retry behavior without creating a fake login path.

Account unlink does not delete encrypted historical references from the event ledger. Users who need physical removal must use the existing Vault hard-purge workflow until a narrower account-reference purge contract is designed.

## Verification

- domain tests reject contact-like or whitespace-bearing values as account references and reject active references in an unlinked record;
- fake-verifier tests cover same-command retry, cross-correlation challenge reuse, rejection, and unavailability;
- SQLite tests cover link, replay, stale revision, unlink, relink, stable local membership, restart restoration, and plaintext-marker absence;
- compile-time port boundaries keep ZDP provider/session types out of domain and application packages.
