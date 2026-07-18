# ADR 0054: Explicit bounded renderer sync control plane

- Status: Accepted
- Date: 2026-07-18

## Context

The backend could enroll devices, exchange immutable packs through a folder, revoke membership, inspect replay outcomes, and bind imported Tasks to local repositories. None of those use cases had a renderer-owned workflow. Exposing a generic file or database API would bypass the product boundary, while making the first status query create a signing key and device membership would turn observation into an undisclosed durable command.

JavaScript numbers also cannot represent every unsigned 64-bit sequence exactly. Returning native JSON numbers for durable sync cursors would eventually make a valid pack range ambiguous in the renderer.

## Decision

- Expose one use-case-specific `SyncService` through Wails for overview, explicit initialization, enrollment, device revocation, folder export/import, and local workspace mapping. Do not expose generic filesystem, Git, SQL, key-store, or pack-decoding methods.
- Keep `Overview` read-only. It may report an uninitialized Vault, but it cannot generate a signing key, register a local device, expire an enrollment, import a pack, or change a workspace mapping.
- Add an explicit `Initialize` command. It creates or recovers the DPAPI-local Ed25519 identity and registers exactly that public membership. Identity-dependent issuer, export, completion, and revocation commands fail with `SYNC_NOT_INITIALIZED` until initialization succeeds.
- Treat accepting an enrollment offer as an explicit bootstrap command for the new Vault. The recipient must create its own device identity while importing the offered Vault key; a status query still cannot do this work.
- Return only public-key fingerprints, device and pack identifiers, lifecycle state, bounded reason codes, local mapping status, and user-selected local paths. Private keys, Vault keys, plaintext pack payloads, event payloads, absolute exchange output paths, and stored acceptance envelopes never cross the Wails response boundary.
- Encode durable unsigned 64-bit device and pack sequences as canonical decimal strings in renderer DTOs. The TypeScript parser rejects leading zeros, overflow-width values, inverted ranges, excess collection sizes, invalid hashes, and malformed timestamps.
- Transfer enrollment packages as bounded base64url only on the explicit create, accept, and complete commands. The UI labels the package secret as a separate high-value transfer and does not persist it in renderer state beyond the current process lifetime.
- Keep folder roots and repository paths as explicit user inputs to narrow use cases. The folder adapter and Git inspector remain the authorities for canonicalization, containment, symlink rejection, baseline verification, and no-clobber publication.
- Bound one overview to 100 devices, 100 enrollments, 50 replay receipts with at most eight reason codes each, and 100 workspace requirements. The renderer validates the same limits before displaying the result.

## Consequences

Opening or refreshing the sync screen no longer changes durable Vault state. A user can see that sync is inactive, initialize it deliberately, exchange enrollment packages, move immutable packs through a chosen folder, revoke another device, inspect conflicts, and map imported Tasks without receiving a generic privileged bridge.

The renderer is still a privileged local surface, not a security sandbox. Clipboard contents, user-selected paths, and enrollment bearer secrets remain sensitive. Git exchange is still separate and cannot be inferred from the folder controls.

## Validation

- Wails tests prove an overview leaves the signing key and device list absent until `Initialize` is called;
- issuer enrollment and export fail closed before explicit initialization;
- initialized overview exposes one local fingerprint without private material;
- Go E2E tests perform explicit issuer initialization and preserve two-Vault enrollment, revocation, folder replay, and restart behavior;
- Svelte diagnostics validate the bounded DTO parsers and renderer state;
- the complete Go suite and production frontend build pass on Windows amd64.
