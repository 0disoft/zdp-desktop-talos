# ADR 0009: Protected Vault Catalog and Reopen

- Status: Accepted
- Date: 2026-07-11
- Owners: ZDP/Talos engineering

## Context

Vault database and key-record filenames are intentionally derived hashes. After restart, the application therefore cannot discover a Vault ID from filenames without either exposing a plaintext index or scanning and guessing protected records. A catalog also becomes part of creation truth: reporting success before registration would create a valid but undiscoverable Vault.

## Decision

- Store the versioned Vault catalog as one current-user DPAPI-protected key-store record under a fixed system reference. Do not write a plaintext catalog, raw Vault ID filename, browser storage copy, or Git projection.
- Catalog entries contain only a UUIDv7 Vault ID and creation time. Accept at most 256 entries so the worst-case encoded document stays below the DPAPI adapter's 64 KiB secret limit.
- Register the catalog entry after the encrypted database commits its initial Vault state. Creation succeeds only after catalog registration.
- If registration fails or has an uncertain result, remove only the exact `(Vault ID, creation time)` entry before removing the database and key. An ID-only cleanup must not delete a pre-existing entry after an improbable UUID collision.
- Reopen in this order: validate UUIDv7, require catalog membership, load the matching DPAPI key, open the hashed database, then verify the stored active Vault identity. Any mismatch closes the database and clears its in-memory key.
- Expose bounded `List` and `Open` Wails methods. The renderer validates UUIDv7, timestamps, result unions, and error shapes before changing UI state.
- Treat Vault IDs as private metadata, not secret payload. They remain visible in SQLite event and state metadata; payload encryption does not claim full database-structure confidentiality.

## Consequences

Talos can now restart, discover protected local Vaults, and reopen one without asking the user to retain an identifier. Catalog corruption, missing keys, missing databases, and stored-identity mismatch fail closed with separate safe error categories.

The catalog read-modify-write lock is process-local. ADR 0010 makes one encrypted-IPC desktop instance the supported writer; CLI and future helper mutation remain forbidden until they gain an explicit ownership protocol.

## Evidence

- catalog ordering, duplicate, exact-removal, and corrupt-document tests;
- catalog-registration compensation tests;
- unknown and mismatched Vault reopen tests;
- local database create-close-reopen tests;
- Windows integration test rebuilding the creator over real DPAPI records and SQLite files;
- Wails list/reopen tests and Svelte diagnostic/build checks.
