# Backup and Restore

Backups are explicit, encrypted, versioned snapshots of the Vault database and every immutable blob referenced by that snapshot. Creation uses the SQLite online backup API instead of copying a live database, WAL, or shared-memory file. The completed snapshot determines the exact blob set; missing, changed, duplicate, symbolic-link, or unexpected files abort publication.

The database, blobs, and versioned manifest are packaged inside one `.talos-backup` stream. A random AES-256-GCM data key encrypts authenticated 1 MiB chunks and is wrapped by the current Vault key. The root key is never copied into the file. Consequently, the current implementation is a same-profile safety copy: losing the DPAPI-protected Vault key makes the backup unusable unless a separately authorized enrollment or recovery path preserved that key.

The destination must be an absolute canonical path with an existing non-link parent and a `.talos-backup` suffix. Talos writes an unpredictable same-directory staging file, flushes it, preflights those exact encrypted bytes, and publishes without overwriting an existing destination. Limits are 64 GiB archive plaintext, 65 GiB encrypted bytes, 100,000 artifacts, and a 32 MiB manifest.

Preflight decrypts into an app-owned temporary directory, validates archive names and sizes, verifies all hashes and encrypted events, runs SQLite `quick_check`, opens the copied Vault through the current migration path, verifies every referenced blob, and checkpoints the copy. Temporary plaintext is removed afterward. A passing result proves only that the current app can decrypt, validate, and forward-migrate an isolated copy. It does not replace the live Vault, prove an older binary can read it, or provide cross-device key recovery.
