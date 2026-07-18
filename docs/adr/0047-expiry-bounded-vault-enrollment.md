# ADR 0047: Expiry-bounded Vault enrollment

- Status: Accepted
- Date: 2026-07-18

## Context

DPAPI binds the Vault key and device signing key to one Windows user profile. Copying those protected blobs to another device cannot open the Vault, while weakening them into plaintext files would turn manual sync into key exfiltration. A target also needs the source signing membership before it can verify packs, and the source needs the independently generated target membership before it can accept packs in return.

An offline file cannot prove global single use. Two machines that have not converged cannot know that the same copied capability was exercised elsewhere. The implementation must not advertise a stronger guarantee than it can enforce.

## Decision

- Generate a 32-byte random enrollment bearer capability. Do not reuse an account credential, user password, DPAPI blob, Vault key, or model-provider secret.
- Encode offer and acceptance as strict, size-bounded JSON packages whose payloads are encrypted with AES-256-GCM under a per-package HKDF-SHA-256 key. Bind package kind and enrollment ID as authenticated data.
- Sign the offer with the source Ed25519 device key. The encrypted offer carries the Vault key, original Vault creation time, current retention setting, source device ID and public key, issue time, and expiry.
- On the target, protect the imported Vault key through the target key store, create or resume the matching encrypted database, register the source, generate a new DPAPI-protected target signing identity, and return a target-signed acceptance encrypted with the same bearer capability.
- On the source, verify the exact offer hash, Vault, source identity, target identity, target signature, and original expiry before registering the target. One issuer enrollment can complete for only one target and exact acceptance hash.
- Add schema 19 `sync_enrollments`. Persist role, state, peer identity, exact package hashes, expiry, event references, and a Vault-encrypted copy of the recipient acceptance. Do not persist the bearer capability, plaintext Vault key, or plaintext offer/acceptance in events or materialized rows.
- Treat identical local retries as idempotent. A target recovers the exact stored acceptance after restart. Changed bytes, a different Vault key under the same Vault ID, a different target after completion, malformed input, unknown fields, invalid signatures, and fresh use after expiry fail closed.
- Keep enrollment events device-local. They are control-plane evidence and remain outside the Task/Decision/Memory export allowlist.

## Consequences

Two independent local Vault databases can establish bidirectional signing trust and exchange canonical packs without copying a DPAPI blob or placing the Vault key in Git. Interrupted target setup can resume from matching protected key/database state, and accepted response bytes are recoverable.

The bearer capability authorizes possession of the encrypted offer until expiry. A copied unconsumed offer may be exercised by another offline holder; only one acceptance can complete on the source, but another holder may already possess the Vault key. UI and file-transfer work must therefore present the capability once, avoid logs and clipboard persistence where possible, use short user-selected validity, and support explicit cancellation or revocation before claiming stronger single-use behavior.

This ADR does not provide workspace-path remapping, cross-device revocation propagation, Git transport, renderer controls, relay sync, or account-driven data sharing.

## Verification

- codec tests prove offer/acceptance round trips, absence of protected plaintext, wrong-secret rejection, strict unknown-field rejection, ciphertext tamper rejection, signature rejection, and expiry enforcement;
- schema-15 fixtures migrate through schema 19 and validate the enrollment table;
- adapter tests prove offer/completion idempotency, changed-target conflicts, encrypted exact acceptance recovery, and tamper detection;
- a two-independent-key-store and two-independent-database scenario establishes both memberships, replays a source Task on the target, replays a target revision on the source, deduplicates the second delivery, and recovers the exact acceptance on retry.
