# ADR 0035: ZDP Account Link Boundary

- Status: Accepted
- Date: 2026-07-15

## Context

Talos participates in ZDP shared signup, but the desktop must not become another owner of credentials, sessions, contact methods, integrated profile data, platform membership, or consent truth. Account linkage also must not imply repository, prompt, model response, diff, memory, or Vault synchronization.

The ZDP desktop product-link contract is now fixed as a contract-only create, browser-complete, and single-use exchange flow. It is not live-handler evidence. A testable local boundary remains required before UI and network code arrive, otherwise provider session types and account policy will leak into Talos domain and storage.

## Decision

- ZDP core remains authoritative for account authentication, session validity, platform membership, consent records, and account audit.
- Talos receives only a normalized verified assertion containing an opaque `subject_ref`, optional `workspace_ref`, `consent_receipt_ref`, and verification time.
- Passwords, session cookies, access or refresh tokens, login IDs, email addresses, phone numbers, nicknames, profile fields, and raw consent documents never cross the inward port.
- The verifier consumes a one-time account-link challenge outside the Talos core. Repeating the same challenge and correlation ID must return the same verified assertion; reuse under a different correlation ID fails closed.
- The production adapter follows `zdp-api-contracts/contracts/apis/core-api/product-link.yaml`: it creates a 32-octet random verifier, sends only its S256 challenge, opens the trusted HTTPS verification URI in the system browser, polls no faster than every five seconds, and exchanges before the ten-minute expiry.
- The proof verifier is ephemeral secret material. It must not enter Vault events, SQLite, logs, traces, crash reports, Wails DTOs, URLs, or frontend state.
- Talos persists one device-local membership per Vault. Link, unlink, and relink use optimistic revisions and idempotency keys. Relink preserves the local membership identity while replacing the active external references.
- Verified references live only in encrypted private event payloads. The materialized `account_links` table stores Vault ID, local membership ID, lifecycle state, revision, timestamps, and event pointer; it stores no raw or hashed account, workspace, contact, session, or consent reference.
- Unlink appends an encrypted tombstone-style event and clears active references from the current record. Historical encrypted events remain until Vault hard purge; unlink is not advertised as physical deletion.
- ZDP verification failure or network unavailability cannot lock an already-open local Vault or block local review and execution that do not require account services.
- Local-only mode is accepted. It permits local Vault and task work but does not enable remote sync, account entitlements, or remote account features.
- A deterministic fake verifier lives under test support only. The S256 HTTP adapter owns verifier generation, trusted-HTTPS browser handoff, bounded polling, standard error-envelope mapping, per-call request IDs, and exchange response filtering. Its bounded HTTP client drops caller cookie jars and never sets Cookie or Authorization. A custom transport can still inject credentials, so production transport construction remains a separate security gate. Production bootstrap and Wails services import neither adapter, so live endpoint wiring and user-facing account controls remain disabled pending deployment and security evidence.
- The production adapter does not reuse `GET /v1/auth/sessions/current` or transport browser session credentials into Talos.

## Upstream Contract

- `zdp-architecture/adr/0027-desktop-product-account-link-handoff.md`
- `zdp-api-contracts/contracts/apis/core-api/product-link.yaml`
- `zdp-api-contracts/docs/contracts/desktop-product-link.md`

## Consequences

The account provider and callback protocol can change without changing the Talos membership model. The implementation remains intentionally dormant in production: fake HTTPS-server tests prove S256 framing, five-second polling, the ten-minute deadline, cancellation, terminal states, trusted verification origins, and secret non-propagation without creating a fake login path.

Account unlink does not delete encrypted historical references from the event ledger. Users who need physical removal must use the existing Vault hard-purge workflow until a narrower account-reference purge contract is designed.

## Verification

- domain tests reject contact-like or whitespace-bearing values as account references and reject active references in an unlinked record;
- fake-verifier tests cover same-command retry, cross-correlation challenge reuse, rejection, and unavailability;
- fake HTTPS-server tests cover exact 201/200 success statuses, standard error envelopes, request/idempotency/trace header separation, `retry_after_seconds` and `Retry-After`, S256 framing, trusted browser URLs, minimum polling, deadline and cancellation, terminal states, caller cookie-jar isolation, and forbidden response fields;
- SQLite tests cover link, replay, stale revision, unlink, relink, stable local membership, restart restoration, and plaintext-marker absence;
- compile-time port boundaries keep ZDP provider/session types out of domain and application packages, while source-boundary tests keep verifier and token fields out of persistence, Wails DTOs, frontend code, URLs, and production assembly.
