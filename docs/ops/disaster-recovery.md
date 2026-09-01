# Disaster Recovery

Local disasters include device loss, Vault corruption, key loss, partial upgrade, disk exhaustion, and unrecoverable worker state. Recovery order is protect existing bytes, stop side effects, verify keys and backup integrity, restore into isolation, migrate, rebuild derivatives, reconcile idempotency/device heads, and only then resume tasks.

ZDP account recovery does not decrypt a Vault. Talos support, repository access, a backup receipt, and possession of ciphertext also provide no decryption authority. The current private-alpha and MVP boundary explicitly treats loss of every authorized Vault-key copy as permanent data loss under ADR 0063.

A local DPAPI loss is recoverable only when another authorized device still holds the Vault key and can create a fresh expiry-bounded enrollment. Enrollment is preventive transfer, not a password-reset service. Complete and test it before device or Windows-profile loss; an expired or copied offer is not durable recovery authority.

The `.talos-backup` flow protects a healthy, open Vault against later database or migration failure and first rehearses recovery in isolation. Live replacement then journals `restore_pending`, verifies the exact target Vault and backup receipt, preserves the original generation, and either validates the promoted generation or rolls back to the validated original. It deliberately omits the Vault root key, so it cannot recover from loss of every enrolled device or authorized DPAPI record.

Hard purge destroys Talos-owned local key material before removing recognized local ciphertext. It cannot erase backups, enrollment files, external disks, clones, Git history, or other copied artifacts. Those copies remain unreadable after total key loss, but Talos does not claim physical overwrite or global deletion of data outside its control.

A user-held encrypted recovery package is not implemented and is not an alpha requirement. Adding one requires a separate accepted ADR covering offline brute-force resistance, explicit provisioning, package epochs, rotation, revocation, rollback, audit, destination key-store import, device membership, crash recovery, and the limits of purging externally held material. Until then, the terminal response to total key loss is fail closed without creating a replacement key for existing ciphertext.
