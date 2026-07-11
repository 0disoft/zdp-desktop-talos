# ADR 0011: Recoverable Encrypted Artifact Staging

- Status: Accepted
- Date: 2026-07-11

## Context

Large prompts, diffs, logs, and reports do not belong in SQLite event rows. Writing ciphertext to a file and then inserting metadata creates two opposite crash hazards: an orphan file when the database write fails, or a ready database row whose file was never durably promoted. A filesystem rename cannot participate in a SQLite transaction.

## Decision

- Add schema version 4 with a `STRICT` artifact metadata table and explicit `staged` and `ready` states.
- Reject empty, secret, malformed, or larger-than-64-MiB payloads before filesystem access.
- Encrypt every artifact with the Vault envelope sealer. Bind Vault ID, artifact UUIDv7, schema version, and sensitivity as authenticated data.
- Write ciphertext to an exclusive mode-0600 staging file under the per-Vault database-owned `.blobs` directory, flush and close it, then insert the staged metadata row.
- Promote the staging file with a same-directory rename and only then mark the row ready. A caller receives success only after the ready transition.
- On every Vault open, reconcile staged rows deterministically. Promote and verify a surviving stage, finish a row whose final file already exists, remove a staged row with no surviving file, and remove only well-formed unreferenced UUIDv7 staging files.
- Verify ciphertext SHA-256 before decryption and plaintext size and SHA-256 after decryption. Missing or mismatched ready data fails closed as corruption.
- Refuse general Vault database removal while retained blob files remain. Hard purge will own recursive artifact deletion under a separate lifecycle and confirmation contract.

## Consequences

SQLite remains authoritative for artifact readiness while ciphertext files remain the large-payload storage medium. Recovery can finish or discard operations that were never reported successful without inventing a cross-resource transaction. Ciphertext uses random envelope keys, so equal plaintext does not imply equal storage filenames; content hashes remain integrity metadata rather than a global deduplication identity.

The current implementation proves local Windows restart behavior and same-filesystem rename behavior. It does not claim network-filesystem durability, secure physical erasure, backup inclusion, or hard-purge completion.

## Evidence

- encrypted round trip and restart without plaintext marker leakage;
- staged-file promotion after simulated crash;
- abandoned row and orphan staging cleanup;
- ciphertext tamper detection and secret-payload rejection;
- database-removal preflight while retained blobs exist.
