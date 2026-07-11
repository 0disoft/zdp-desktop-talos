# ADR 0006: Ledger Migrations and Idempotency Integrity

- Status: Accepted
- Date: 2026-07-11
- Owners: ZDP/Talos engineering

## Context

The Phase 0 ledger created tables idempotently but did not record a database schema version. It also mapped an idempotency key only to an event ID. Reusing the same key with a different event body therefore returned the old event as if the new request had succeeded. That behavior prevents duplicate effects but silently lies about which request was accepted.

## Decision

- Track the per-Vault SQLite schema with monotonic `PRAGMA user_version` migrations. Apply each migration in its own transaction and reject databases newer than the application.
- Treat the original unversioned Phase 0 schema as version 1. Migration 2 adds a nullable request fingerprint to existing idempotency rows without rewriting encrypted historical payloads.
- Validate required tables and columns after migrations. A matching version number without the required schema is corruption, not compatibility.
- Hash the canonical append intent with SHA-256 before generating server-owned timestamps. The fingerprint covers Vault ID, event type, payload schema version, sensitivity, payload bytes, and an explicitly supplied occurrence time.
- An idempotency key may return the previous result only when its stored fingerprint matches the current request. A different fingerprint returns a conflict and performs no write.
- Legacy rows without a fingerprint fail closed as unverifiable. They are not replayed and are not silently treated as a match.
- Validate sensitivity and the secret-payload prohibition before looking up idempotency state so an invalid retry cannot inherit an older successful result.
- Keep migrations forward-only. Future destructive changes require a separate backup, compatibility, and roll-forward decision.

## Consequences

Existing Phase 0 databases migrate without decrypting or rewriting events. A retry against a legacy idempotency row now requires reconciliation and a new command identity instead of returning an unprovable result. This is deliberately less convenient than guessing because the ledger is an execution authority.

The migration runner is intentionally local to the SQLite adapter. Domain and application packages remain unaware of SQLite versions and SQL syntax.

## Evidence

- same-key same-request replay returns one event;
- same-key different-request replay returns an idempotency conflict and preserves the first event;
- invalid secret retries are rejected before idempotency lookup;
- unversioned Phase 0 databases migrate to schema version 2;
- legacy hashless idempotency rows fail closed;
- newer and structurally malformed schemas are rejected.
