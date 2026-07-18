# ADR 0056: Encrypted Vault backup and isolated migration preflight

- Status: Accepted
- Date: 2026-07-19

## Context

The Vault database is authoritative local state. Copying its main SQLite file while WAL pages may still be live can omit committed data, and copying the database without its immutable artifact blobs produces a snapshot that opens but cannot recover task evidence. App-level event encryption also does not encrypt plaintext SQLite indexes and materialized metadata, so placing an ordinary database copy in a backup folder would overstate confidentiality.

The DPAPI Vault key is bound to the current Windows user profile. Putting that root key into the backup would turn one copied file into both ciphertext and decryption authority. Omitting the key means a backup can be usable only while the same Vault key remains recoverable on the device or through the separately authorized enrollment path.

## Decision

- Create the database snapshot with the SQLite online backup API. Do not copy the live main database, WAL, or shared-memory files directly.
- Read artifact references from the completed snapshot and include only exact ready immutable blobs whose ciphertext hashes match the database. Fail before publication on a missing, changed, duplicate, symbolic-link, or unexpected artifact.
- Package the snapshot database, required blobs, and a versioned manifest inside one encrypted stream. The manifest records the backup ID, Vault and key identity, source application and schema versions, creation time, file sizes, and SHA-256 hashes; none of that plaintext appears outside the encrypted stream.
- Encrypt in 1 MiB authenticated chunks with one random AES-256-GCM data key wrapped by the Vault KEK. Bind every frame to the exact header, index, flags, and plaintext length. Require one authenticated empty terminal frame and reject truncation, reordering, modification, or trailing bytes.
- Accept only an absolute canonical `.talos-backup` path with an existing non-link parent. Create an unpredictable same-directory staging file, flush it, run a complete preflight against those exact bytes, and promote without overwriting an existing destination.
- Bound a backup at 64 GiB of archive plaintext, 65 GiB of encrypted file bytes, 100,000 artifact entries, and a 32 MiB manifest. These are denial-of-service ceilings, not product capacity promises.
- Preflight into an app-owned temporary directory. Validate archive entry names, sizes, hashes, SQLite `quick_check`, Vault identity, every encrypted event, every referenced artifact, and the current forward migration path. Delete the temporary plaintext snapshot after validation.
- Expose backup creation and preflight as use-case-specific Vault methods. A Vault must be open, the same key must be available, and switching or locking the Vault clears renderer paths and receipts.
- Do not replace the live Vault in this slice. A passing preflight proves that an isolated copy can be decrypted, validated, and migrated by the current application; it is not permission to overwrite operational data or claim cross-device recovery.

## Consequences

A published backup is confidential even though the underlying SQLite materialized state is not fully encrypted at rest. The file does not contain a plaintext root key and therefore cannot recover a lost Windows profile by itself. Hard purge cannot erase copied backups, and deleting a backup does not guarantee physical overwrite on SSDs or external media.

Backup creation performs an online database snapshot and then a full restore rehearsal, so it is intentionally heavier than ordinary Vault operations. The Vault service serializes the operation against renderer-initiated writes. SQLite still owns concurrent snapshot semantics if an already-running internal operation finishes during backup.

## Validation

- multi-chunk encryption round trips without exposing plaintext markers;
- wrong key, wrong key ID, tampering, truncation, missing terminal frame, and trailing bytes fail closed;
- SQLite online backup includes committed WAL state and detects a missing artifact;
- backup creation includes database and blob ciphertext, publishes no plaintext Vault ID or payload marker, and returns a hash receipt;
- isolated preflight opens the copied database, verifies every event and blob, runs migrations, checkpoints the copy, and leaves the live Vault unchanged;
- duplicate destinations, relative paths, wrong Vaults, wrong keys, and modified backup files fail without overwriting user files;
- Wails DTOs do not expose keys, internal staging paths, database paths, or raw payloads.
