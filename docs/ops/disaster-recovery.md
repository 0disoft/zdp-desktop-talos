# Disaster Recovery

Local disasters include device loss, Vault corruption, key loss, partial upgrade, disk exhaustion, and unrecoverable worker state. Recovery order is protect existing bytes, stop side effects, verify keys and backup integrity, restore into isolation, migrate, rebuild derivatives, reconcile idempotency/device heads, and only then resume tasks.

ZDP account recovery does not decrypt a Vault unless a separately designed recovery mechanism explicitly provides that capability. The product must state whether a lost key means permanent local-data loss; it must not promise server recovery by implication.

The `.talos-backup` flow protects a healthy, open Vault against later database or migration failure and first rehearses recovery in isolation. Live replacement then journals `restore_pending`, verifies the exact target Vault and backup receipt, preserves the original generation, and either validates the promoted generation or rolls back to the validated original. It deliberately omits the Vault root key, so it still cannot recover from loss of the Windows profile or DPAPI record.
