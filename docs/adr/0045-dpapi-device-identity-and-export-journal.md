# ADR 0045: DPAPI device identity and export journal

- Status: Accepted
- Date: 2026-07-18

## Context

A pack codec and trusted public-key membership do not prove that the sending device can recover its private signing identity after restart. Export also needs a durable assignment from canonical local events to one contiguous device sequence. Building a pack directly from a query would allow a crash or concurrent retry to assign different sequence numbers or produce different bytes without a recovery record.

## Decision

- Keep one Ed25519 private signing key per Vault and local installation under the existing current-user DPAPI key store. Derive the device ID from the SHA-256 digest of the public key and persist only the public key in SQLite membership state.
- Recover an existing private key before generating a new one. A matching active membership is reused; corrupt key bytes, public-key mismatch, or revoked membership fail closed.
- Add schema 17 export state: event origins, per-device export heads, export batches, and batch-event assignments. A preparing batch reserves one contiguous sequence range atomically and is returned unchanged after restart.
- Exclude local `sync.*` control events from export so validation and export receipts do not recursively manufacture new packs. Canonical product events, including `vault.created`, remain exportable.
- Run the deterministic secret scanner over every event payload before encryption. Any finding or scanner failure leaves the reservation pending and produces no ready pack.
- Finalize a batch only when Vault, device, sequence range, count, pack identity, and ciphertext hash match the reservation. Store the exact ready pack under the local Vault envelope so retries and later downloads return identical bytes.
- Delete the local signing key during Vault hard purge. Never place the private key in SQLite, a pack, a renderer DTO, logs, or Git.

## Consequences

Talos can now recover one local device identity and create restart-safe signed exports without plaintext key files or sequence reassignment. A ready pack is still only an immutable exchange artifact. Import replay, materialized-state conflict handling, device enrollment transfer, and Git transport remain separate gates.

## Verification

- schema 15 upgrades through schema 17 and validates all export tables;
- reservation replay returns the original event set and sequence range;
- ready pack bytes and event payload markers remain encrypted in SQLite across checkpoint and restart;
- application tests prove one signing key is reused and exported sequences remain contiguous after restart;
- JSON credential-shaped payloads are caught by the shared scanner and block finalization.
