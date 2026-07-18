# ADR 0043: Durable sync membership and validation journal

- Status: Accepted
- Date: 2026-07-18

## Context

A cryptographically valid pack is not enough to authorize an import. The verifier must obtain the Ed25519 public key from a separately trusted Vault membership record, reject revoked devices, prevent sequence gaps and overlaps, and survive a crash after validation without losing the exact pack bytes.

## Decision

- Persist Vault-scoped device membership with an Ed25519 public key, active or revoked state, optimistic revision, and the next contiguous import sequence.
- Resolve the verification key from membership before invoking the pack codec. A key supplied by the pack or caller is never an authorization source.
- Advance the device cursor only with a conditional SQLite update in the same transaction that appends the local `sync.pack.validated` event and validation receipt.
- Store the exact validated pack under the local Vault envelope key. A repeated byte-identical pack returns the original receipt; a reused pack identity with different bytes or metadata fails closed.
- Keep revocation and pack validation event-backed and restart-safe. A revoked device cannot add a new pack, while an already recorded pack remains readable as historical evidence.
- Treat `validated` as a staging state. Validation does not mean remote events were applied to canonical streams or materialized state.

## Consequences

Talos can now authenticate a sending device and durably journal contiguous immutable packs without duplicate delivery. ADR 0045 supplies local signing-key persistence and export journaling. Talos still cannot claim completed multi-device sync until Vault enrollment transfer, ordered event application, conflict surfacing, Git transport, and cross-device revocation propagation exist.

## Verification

- schema 15 upgrades through schema 17 with validation and export state;
- registration and revocation are revisioned, idempotent, encrypted event transitions;
- exact pack replay is idempotent while changed bytes, gaps, overlaps, and revoked devices fail closed;
- validated pack bytes remain encrypted in SQLite and survive checkpoint plus restart;
- the importer verifies with the membership key and never journals an untrusted signature.
