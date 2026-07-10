# Disaster Recovery

Local disasters include device loss, Vault corruption, key loss, partial upgrade, disk exhaustion, and unrecoverable worker state. Recovery order is protect existing bytes, stop side effects, verify keys and backup integrity, restore into isolation, migrate, rebuild derivatives, reconcile idempotency/device heads, and only then resume tasks.

ZDP account recovery does not decrypt a Vault unless a separately designed recovery mechanism explicitly provides that capability. The product must state whether a lost key means permanent local-data loss; it must not promise server recovery by implication.
