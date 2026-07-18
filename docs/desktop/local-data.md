# Local Data

- Status: Accepted baseline
- Owner: Talos desktop runtime

## Storage

Each Vault uses one local SQLite database for events, materialized state, idempotency records, and artifact metadata. Large prompts, diffs, logs, and reports live as content-addressed encrypted blobs. Search indexes, embedding caches, and model summaries are disposable local derivatives.

Artifact ciphertext is stored under the opaque per-Vault database path in a sibling `.blobs` directory. Files are staged, flushed, recorded, promoted, and marked ready in separate recoverable steps because filesystem rename cannot join a SQLite transaction. Startup reconciliation never treats an incomplete stage as a ready artifact, and reads verify ciphertext and plaintext hashes. Individual payloads are currently bounded to 64 MiB.

Event and state updates share one database transaction. WAL files, checkpoints, backups, and crash recovery are part of the durability contract, not implementation trivia.

The schema-version-3 Vault read model stores retention and lifecycle metadata with optimistic revisions and a reference to its last event. It does not store whether a Vault is unlocked. Unlock authority is process-local and must be re-established from the OS key store after every restart.

Schema version 15 adds optional expiry and replacement identifiers to materialized memory lifecycle state. Statements, rationale, applicability, evidence, and transition reasons remain only in encrypted events. Expired active rows are excluded from context queries before an explicit sweep transitions them to `stale`.

Schema version 5 adds Task metadata and immutable contract-revision pointers. The actual workspace path, contract goals, scopes, forbidden actions, and acceptance criteria remain only in encrypted event payloads; plaintext tables retain a workspace-path hash, baseline, and provenance needed for bounded lifecycle queries and integrity constraints.

Schema version 6 adds Decision state and answer pointers. Question text, rationale, safe defaults, scopes, options, and answer values remain encrypted; plaintext rows retain revisions, repository baselines, answer hashes, state, and provenance required for stale-answer and conflict enforcement.

On Windows, the application bootstrap keeps protected key records and per-Vault databases under separate children of `%LocalAppData%/0disoft/Talos Agent`. Creation is a compensated workflow: a database or initial-state failure removes any partial database and the newly protected key. Locking closes the database and clears the envelope sealer's in-memory key copy.

The Vault discovery catalog is a versioned DPAPI-protected record, and catalog/key filenames are hashes rather than raw Vault IDs. Vault IDs still exist as SQLite event and state metadata; payload encryption does not claim to hide database structure or all metadata from a process that can read the local database file.

The desktop runtime enforces one writer instance per OS user. Its Wails second-instance notification uses a separate random DPAPI-protected IPC key; notification arguments and working-directory metadata are ignored rather than logged. If that key cannot be loaded, Vault mutation stays unavailable.

## Encryption and Keys

The OS key store protects a Vault key-encryption key. Each stored event payload receives a random 256-bit data key and AES-256-GCM nonce; that data key is separately wrapped by the key-encryption key with authenticated context binding the Vault, object, schema, and sensitivity. Worker processes do not receive the Vault root key. Platforms without a supported secure key store fail with an explicit unsupported error; plaintext key-file fallback is forbidden.

On Windows, Talos stores Vault key-encryption keys with current-user DPAPI and forbids the machine-wide scope. Vault and key identifiers are hashed for filenames and bound into DPAPI optional entropy, so moving a protected record to another Vault or key reference cannot unseal it. Writes and rotations use same-directory temporary files and Windows atomic replacement APIs. The doctor path verifies put, reopen, rotate, delete, and plaintext-marker absence without printing key material.

The event-envelope test path still uses an ephemeral in-memory key to isolate ciphertext integrity, restart behavior, wrong-key rejection, and plaintext-marker absence from the OS adapter. macOS and Linux key-store adapters remain unsupported and must fail explicitly.

## Classification

`public`, `private`, `sensitive`, and `secret` are mandatory classifications. Secret values are never stored. A finding stores only detector identity, HMAC-style fingerprint, bounded location, and redaction action.

## Deletion

Logical deletion creates a tombstone and immediately excludes data from context and projections. Hard purge destroys payload keys and blobs, checkpoints and compacts storage as required, regenerates projections, and reports the limits of local backups and remote Git history. The UI must not describe a tombstone as physical deletion.

Device-local hard purge requires the current Vault revision and exact Vault ID. Talos first closes the database, records `purge_pending` in the protected catalog, destroys the DPAPI Vault key, removes recognized ciphertext and SQLite files, and removes the journal entry. Startup resumes an interrupted purge before listing Vaults. Key destruction is cryptographic erasure; filesystem unlink is not advertised as SSD overwrite, and backups, exports, clones, or Git history are not erased by this local action.
