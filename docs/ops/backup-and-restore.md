# Backup and Restore

Backups are encrypted, versioned, integrity-checked snapshots of the Vault database, required blobs, and key-envelope metadata. Root keys are not copied as plaintext into the backup. Backup creation coordinates SQLite WAL/checkpoint state and records the schema and application compatibility range.

Restore is tested into a separate location before replacement, verifies ciphertext and references, preserves tombstones, rebuilds disposable indexes/projections, and reconciles device sequence state. A backup without a recoverable key path is reported as unusable rather than accepted.
