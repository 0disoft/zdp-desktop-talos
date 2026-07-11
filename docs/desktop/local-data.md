# Local Data

- Status: Accepted baseline
- Owner: Talos desktop runtime

## Storage

Each Vault uses one local SQLite database for events, materialized state, idempotency records, and artifact metadata. Large prompts, diffs, logs, and reports live as content-addressed encrypted blobs. Search indexes, embedding caches, and model summaries are disposable local derivatives.

Event and state updates share one database transaction. WAL files, checkpoints, backups, and crash recovery are part of the durability contract, not implementation trivia.

The schema-version-3 Vault read model stores retention and lifecycle metadata with optimistic revisions and a reference to its last event. It does not store whether a Vault is unlocked. Unlock authority is process-local and must be re-established from the OS key store after every restart.

## Encryption and Keys

The OS key store protects a Vault key-encryption key. Each stored event payload receives a random 256-bit data key and AES-256-GCM nonce; that data key is separately wrapped by the key-encryption key with authenticated context binding the Vault, object, schema, and sensitivity. Worker processes do not receive the Vault root key. Platforms without a supported secure key store fail with an explicit unsupported error; plaintext key-file fallback is forbidden.

On Windows, Talos stores Vault key-encryption keys with current-user DPAPI and forbids the machine-wide scope. Vault and key identifiers are hashed for filenames and bound into DPAPI optional entropy, so moving a protected record to another Vault or key reference cannot unseal it. Writes and rotations use same-directory temporary files and Windows atomic replacement APIs. The doctor path verifies put, reopen, rotate, delete, and plaintext-marker absence without printing key material.

The event-envelope test path still uses an ephemeral in-memory key to isolate ciphertext integrity, restart behavior, wrong-key rejection, and plaintext-marker absence from the OS adapter. macOS and Linux key-store adapters remain unsupported and must fail explicitly.

## Classification

`public`, `private`, `sensitive`, and `secret` are mandatory classifications. Secret values are never stored. A finding stores only detector identity, HMAC-style fingerprint, bounded location, and redaction action.

## Deletion

Logical deletion creates a tombstone and immediately excludes data from context and projections. Hard purge destroys payload keys and blobs, checkpoints and compacts storage as required, regenerates projections, and reports the limits of local backups and remote Git history. The UI must not describe a tombstone as physical deletion.
