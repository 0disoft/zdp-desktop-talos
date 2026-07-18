# Disaster Recovery

Local disasters include device loss, Vault corruption, key loss, partial upgrade, disk exhaustion, and unrecoverable worker state. Recovery order is protect existing bytes, stop side effects, verify keys and backup integrity, restore into isolation, migrate, rebuild derivatives, reconcile idempotency/device heads, and only then resume tasks.

ZDP account recovery does not decrypt a Vault unless a separately designed recovery mechanism explicitly provides that capability. The product must state whether a lost key means permanent local-data loss; it must not promise server recovery by implication.

The current `.talos-backup` flow protects a healthy, open Vault against later database or migration failure and rehearses recovery only in isolation. It deliberately omits the Vault root key, so it cannot recover from loss of the Windows profile or DPAPI record. It also does not replace live files. A future replacement workflow must journal every step, verify the exact target Vault and backup receipt, preserve the original bytes, and fail closed before any irreversible mutation.
