# ADR 0012: Restart-Safe Vault Hard Purge

- Status: Accepted
- Date: 2026-07-11

## Context

A Vault hard purge crosses four independently failing stores: the open SQLite handle, the DPAPI-protected Vault key, encrypted blob and database files, and the protected discovery catalog. Deleting those resources in an ad hoc order can leave an open key for partially deleted data, a discoverable Vault with no key, or an invisible Vault whose ciphertext is never cleaned up. Filesystem deletion also cannot honestly promise physical overwrite on SSDs, snapshots, backups, or remote Git history.

## Decision

- Upgrade the DPAPI-protected catalog document to version 2 and give every entry an explicit `active` or `purge_pending` state. Version-1 entries load as active and are rewritten as version 2 on the next mutation.
- Require an open Vault, the current optimistic revision, and an exact Vault-ID confirmation before starting hard purge.
- Close the SQLite session before recording the irreversible transition. If close or catalog transition fails, no key or ciphertext is deleted and the Vault can be reopened.
- Atomically change the protected catalog entry from active to purge pending before destructive work. Pending entries are excluded from normal listing and opening.
- After the pending transition, delete the DPAPI Vault key first, then delete only recognized `.blob` and `.stage` files under the derived per-Vault artifact directory, remove SQLite/WAL/SHM files, and finally remove the pending catalog entry.
- Treat key-not-found and already-removed files as successful replay. Every desktop bootstrap reconciles all pending purges before exposing Vault services.
- Reject unexpected files, directories, symlinks, or reparse-like entries in the artifact directory instead of broad recursive deletion. A blocked purge fails startup closed and preserves the unexpected evidence for operator review.
- Return `VAULT_PURGE_INCOMPLETE` when the journal exists but cleanup cannot finish. The UI locks the session and explains that startup recovery will retry.

## Consequences

Once `purge_pending` is durable, purge is intentionally irreversible and restart-replayable. Destroying the only local Vault KEK provides cryptographic erasure of encrypted payloads even if unlinking ciphertext later fails. File removal is best effort and is not described as secure overwrite. Local or remote backups, exported projections, Git history, and copied files remain outside this device-local guarantee and require their own deletion workflow.

The catalog is both discovery index and purge journal, avoiding a second protected document whose state could disagree. A malformed pending purge blocks Vault bootstrap rather than silently restoring or ignoring a partially destroyed Vault.

## Evidence

- catalog v1 compatibility, v2 rewrite, active/pending separation, and idempotent pending transition;
- exact confirmation and optimistic revision tests;
- key-deletion failure followed by successful reconciliation;
- unexpected artifact filename blocks destructive deletion;
- real Windows create, encrypted artifact restart, hard purge, and empty post-restart catalog;
- Wails and Svelte confirmation flow with stable safe errors.
