# Local Data

- Status: Accepted baseline
- Owner: Talos desktop runtime

## Storage

Each Vault uses one local SQLite database for events, materialized state, idempotency records, and artifact metadata. Large prompts, diffs, logs, and reports live as content-addressed encrypted blobs. Search indexes, embedding caches, and model summaries are disposable local derivatives.

Event and state updates share one database transaction. WAL files, checkpoints, backups, and crash recovery are part of the durability contract, not implementation trivia.

## Encryption and Keys

The OS key store protects a Vault key-encryption key. Per-object random data keys encrypt payloads and artifacts. Worker processes do not receive the Vault root key. Platforms without a supported secure key store fail with an explicit unsupported error; plaintext key-file fallback is forbidden.

## Classification

`public`, `private`, `sensitive`, and `secret` are mandatory classifications. Secret values are never stored. A finding stores only detector identity, HMAC-style fingerprint, bounded location, and redaction action.

## Deletion

Logical deletion creates a tombstone and immediately excludes data from context and projections. Hard purge destroys payload keys and blobs, checkpoints and compacts storage as required, regenerates projections, and reports the limits of local backups and remote Git history. The UI must not describe a tombstone as physical deletion.
