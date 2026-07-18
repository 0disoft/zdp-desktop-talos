# Local Data

- Status: Accepted baseline
- Owner: Talos desktop runtime

## Storage

Each Vault uses one local SQLite database for events, materialized state, idempotency records, and artifact metadata. Large prompts, diffs, logs, and reports live as content-addressed encrypted blobs. Search indexes, embedding caches, and model summaries are disposable local derivatives.

Artifact ciphertext is stored under the opaque per-Vault database path in a sibling `.blobs` directory. Files are staged, flushed, recorded, promoted, and marked ready in separate recoverable steps because filesystem rename cannot join a SQLite transaction. Startup reconciliation never treats an incomplete stage as a ready artifact, and reads verify ciphertext and plaintext hashes. Individual payloads are currently bounded to 64 MiB.

Event and state updates share one database transaction. WAL files, checkpoints, backups, and crash recovery are part of the durability contract, not implementation trivia.

Manual Vault backup uses the SQLite online backup API and captures the exact ready artifact blobs referenced by the resulting snapshot. The snapshot, blobs, and manifest are contained in a chunk-authenticated encrypted `.talos-backup` file; the DPAPI-protected Vault root key is not included. Isolated preflight verifies and forward-migrates a temporary copy with the current binary, then removes it. Explicit live restore uses authenticated deterministic stage and previous-generation directories plus a DPAPI-protected catalog state; neither operation turns the backup into cross-profile recovery media.

A prepared update stores one path-free `talos.update-preparation/1` record under the Vault's current-user DPAPI boundary. It contains release, signing-key, installer, Vault-revision, and encrypted-backup identities and hashes, but no manifest, installer, backup, stage, or user directory path. Preparing another release replaces that record. Live restore invalidates it before changing generations, and hard purge deletes it with the Vault key and device signing key.

The schema-version-3 Vault read model stores retention and lifecycle metadata with optimistic revisions and a reference to its last event. It does not store whether a Vault is unlocked. Unlock authority is process-local and must be re-established from the OS key store after every restart.

Schema version 15 adds optional expiry and replacement identifiers to materialized memory lifecycle state. Statements, rationale, applicability, evidence, and transition reasons remain only in encrypted events. Expired active rows are excluded from context queries before an explicit sweep transitions them to `stale`.

Schema version 5 adds Task metadata and immutable contract-revision pointers. The actual workspace path, contract goals, scopes, forbidden actions, and acceptance criteria remain only in encrypted event payloads; plaintext tables retain a workspace-path hash, baseline, and provenance needed for bounded lifecycle queries and integrity constraints.

Schema version 20 adds a stable Vault-bound workspace identifier to Task state and a device-local workspace mapping journal. The plaintext mapping row contains only source and local-root hashes, baseline, lifecycle revision, state, timestamps, and event references. Canonical local paths remain in encrypted mapping events. Local Task creation binds its inspected root automatically; imported Tasks receive the stable identity without a local path and fail closed until an explicit remap verifies that the selected Git repository contains the immutable baseline. Mapping events are control-plane records and are never exported.

Schema version 21 records the bridge from each local legacy Task v1 contract event to a path-free Task v2 sync snapshot. New Task events contain only the stable workspace identifier and source hash, never the absolute root. Existing local revisions receive deterministic one-per-revision bridge records while imported v1 events do not generate another exportable snapshot. The original event and contract pointer remain unchanged; the bridge exists only to preserve old local contracts across manual sync.

Schema version 22 adds the stable workspace identifier to materialized Memory records and records one path-free Memory v2 snapshot for each locally originated legacy Memory revision. New Memory events carry only `workspace_id` and the source hash; context assembly compares that identifier rather than a local path hash. Imported v1 events never create new snapshots, and the original Memory event chain remains unchanged.

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

Device-local hard purge requires the current Vault revision and exact Vault ID. Talos first closes the database, records `purge_pending` in the protected catalog, destroys the DPAPI Vault key, device signing key, and protected update preparation, removes recognized ciphertext and SQLite files, and removes the journal entry. Startup resumes an interrupted purge before listing Vaults. Key destruction is cryptographic erasure; filesystem unlink is not advertised as SSD overwrite, and backups, exports, clones, or Git history are not erased by this local action.

Memory projection is not canonical storage. The current compiler emits byte-stable public-only Markdown, YAML, and JSONL views, excludes local Vault and workspace identifiers, and rejects the whole result when scanning finds a likely secret. Manual sync packs use a separate path: the private device key stays in DPAPI, event-to-sequence assignments and exact ready packs stay encrypted in the Vault database, and a secret finding prevents pack finalization. Schema 18 records per-event applied, conflicted, or quarantined replay outcomes. Schema 19 records expiry-bounded enrollment offer/acceptance state and keeps the exact acceptance under the Vault envelope for restart-safe retry. Schema 23 records terminal cancellation or expiry, reconciles elapsed deadlines on open, and clears the recipient acceptance envelope. Schema 20 records only hashed device-local workspace mapping state in plaintext and keeps local roots encrypted. The random enrollment secret and plaintext Vault key are never persisted in the enrollment journal. Imported event payloads remain encrypted, preserve their remote identity and device sequence, and cannot be exported again. Folder exchange writes only encrypted immutable packs beneath hashed identity segments and treats the chosen directory as disposable transport state. Git exchange uses the same bytes under `talos-sync/`, writes from a clean branch, and imports only tracked files from an unchanged clean commit; Talos never persists remote credentials or performs commit, pull, or push. The renderer's overview query does not create keys or membership; only explicit initialization does. Durable sequence values cross the JavaScript boundary as decimal strings.
