# ADR 0042: Signed encrypted immutable sync pack

- Status: Accepted
- Date: 2026-07-18

## Context

Copying one shared JSONL file between devices would create merge conflicts and provide no proof of device ownership, event order, duplicate delivery, or ciphertext integrity. A Git commit hash also does not replace a device signature because Git transport and Vault membership have different trust boundaries.

## Decision

- Define versioned pack, manifest, and encrypted payload schemas.
- Bind each pack to one Vault, one device, and one contiguous inclusive `device_seq` range. Gaps, reordering, zero sequences, and mismatched event counts are rejected before import.
- Encrypt the complete event batch with the Vault sync key using the existing envelope primitive and AAD containing the Vault, device sequence range, schema version, and sensitivity.
- Derive `pack_id` from Vault, device, sequence range, and ciphertext hash. Sign the canonical manifest with Ed25519. Import requires the expected membership public key; a key carried only by the pack is never trusted.
- Bound a pack to 512 events and 16 MiB. Verify outer schema, Vault/device identity, pack identity, ciphertext hash, signature, decryption, payload schema, event validation, and contiguous sequence in that order.
- Keep the codec independent from SQLite and Git. Membership/revocation, validation journaling, and duplicate-range rejection are supplied by ADR 0043; local key persistence and export journaling are supplied by ADR 0045. Materialized-state replay and conflict surfacing remain later layers.

## Consequences

Talos now has a concrete encrypted exchange object instead of a filename convention. ADR 0043 adds durable authorization and validation receipts, but a validated pack still cannot mutate canonical streams until ordered replay and conflict handling exist. Git remains a future transport for immutable blobs, not the authority that validates them.

## Verification

- round-trip tests cover encrypted private and sensitive event payloads;
- encoded-pack scans prove plaintext payload markers are absent;
- negative tests cover wrong Vault, wrong device, wrong key, ciphertext tampering, signature failure, and sequence gaps;
- the full Go suite exercises the codec with the existing envelope implementation.
