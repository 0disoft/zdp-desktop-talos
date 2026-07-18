# ADR 0057: Journaled live Vault restore with verified rollback

- Status: Accepted
- Date: 2026-07-19

## Context

A Vault generation is not one file. The authoritative SQLite database, possible WAL sidecars, and immutable artifact directory must agree. Renaming those paths one at a time is not atomic, and a process interruption between renames can leave a mixture that must never be opened as an ordinary active Vault. Isolated preflight proved backup readability but did not establish a safe live replacement contract.

## Decision

- Require a successful preflight and bind restore to its exact backup ID and ciphertext SHA-256, the current Vault revision, and an exact Vault-ID confirmation.
- Extract and migrate into an app-owned deterministic stage generation. Authenticate the stage metadata with a purpose-separated HMAC key derived from the Vault KEK. Do not persist the user-selected source path or root key.
- Record `restore_pending` in the DPAPI-protected Vault catalog before moving live files. Active listing and ordinary open exclude that state.
- Preserve the complete current generation under a deterministic `previous` directory before activating any staged component. Never overwrite an existing stage, previous generation, live database, or artifact directory.
- Treat stage-component presence as the restart cursor. Reconciliation can resume after the old database, old blobs, new database, or new blobs were moved without repeating a completed rename.
- Open the promoted database with the current key, run migrations, verify Vault identity and state, decrypt every event and referenced artifact, checkpoint, and compare counts with authenticated stage metadata.
- If the promoted generation fails validation, remove only recognized new-generation files, restore the preserved generation, and validate it before returning the catalog to `active`.
- Mark the catalog active only after a valid restored or rolled-back generation exists. Cleanup of stage and previous generations is strict, restart-retried, and rejects unknown files, directories, and links.
- Reconcile pending restores during bootstrap before exposing Vault services. A state that cannot establish either a valid restored generation or a valid original generation fails startup closed.

## Consequences

Live restore intentionally discards canonical events and settings created after the backup. The UI therefore presents it only after preflight and requires explicit identity confirmation. A rolled-back restore returns the original Vault open with a distinct outcome; an incomplete restore keeps the Vault unavailable until startup reconciliation succeeds.

The protected catalog is the operation journal, while file presence is the idempotent step cursor. Neither is advertised as protection from same-user malware. This mechanism still depends on recoverability of the same Vault key and does not make the backup a cross-profile recovery package.

## Validation

- exact confirmation and preflight identity are required before journaling;
- stage metadata tampering fails HMAC verification;
- interruption after each database/blob preservation and activation step resumes to the same restored generation;
- a corrupted promoted database rolls back to the newer original generation;
- startup reconciliation completes a journaled but unapplied restore;
- unknown restore-directory entries fail cleanup closed;
- Wails responses omit keys, source database paths, stage paths, previous paths, and raw payloads.
