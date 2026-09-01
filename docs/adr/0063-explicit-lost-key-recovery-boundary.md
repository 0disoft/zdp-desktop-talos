# ADR 0063: Explicit lost-key recovery boundary

- Status: Accepted
- Date: 2026-09-01
- Owners: ZDP/Talos product and engineering

## Context

Talos protects each Vault key through the current Windows user's DPAPI boundary. Expiry-bounded enrollment can transfer that key to another independently protected device while an authorized source still exists, and encrypted backups preserve Vault ciphertext without embedding the root key. Those controls protect confidentiality, but they do not create a password-reset path after every authorized copy of the Vault key has been lost.

Putting the root key in the backup, ordinary settings, Git, relay storage, logs, or an account service would collapse the local-encryption boundary. Treating a ZDP login or support intervention as recovery authority would create the same hidden escrow while implying that account recovery decrypts product data.

Private alpha needs a truthful failure contract now. A user-held encrypted recovery package may be evaluated later, but it must not be improvised as a weak fallback or presented as implemented recovery.

## Threat model

The boundary must withstand a copied backup, stolen recovery artifact, offline password guessing, stale or replayed recovery material, rollback to an older backup, compromised ZDP account, support impersonation, same-user malware, and loss of every enrolled device. It must also avoid claiming that Talos can erase or revoke copies stored outside Talos-owned paths.

## Decision

### Current support boundary

- Private alpha and MVP do not support recovery after every authorized Vault-key copy has been lost. The Vault database, immutable artifacts, and `.talos-backup` files are then permanently unreadable.
- Talos support, a ZDP account session, account recovery, repository access, a backup receipt, and possession of ciphertext do not authorize decryption or key reset.
- The root key, plaintext recovery key, DPAPI blob, enrollment bearer capability, or device-signing private key must not be copied into backups, Git, relay storage, ordinary application settings, logs, diagnostics, telemetry, renderer state, or account records.
- Existing enrollment is preventive transfer, not post-loss recovery. It works only while an authorized source can still create a fresh offer and the target completes it within the bounded enrollment contract.
- Product copy and recovery runbooks must state that a backup does not contain recovery authority, loss of every authorized key causes permanent data loss, and a second-device enrollment must be completed and tested before it is relied on as a continuity control.

### Current recovery state model

- `key_available`: the current protected key opens the Vault. Backup, preflight, restore, and fresh enrollment may proceed under their existing contracts.
- `local_key_lost`: this profile cannot open the Vault. A surviving authorized device may create a fresh enrollment; a backup or ZDP login alone cannot repair the profile.
- `all_keys_lost`: no authorized device can produce the Vault key or a fresh enrollment. This state is terminal in the current product. Talos must fail closed and must not create a replacement key for the existing ciphertext.
- Talos cannot reliably prove that every remote copy is gone. It therefore reports the local failure it can observe and documents the terminal condition instead of claiming global key inventory.

### Rotation, revocation, audit, and hard purge

- DPAPI record rotation and device revocation continue to use their existing local and signed cross-device contracts. Neither operation creates recovery authority.
- Hard purge deletes Talos-owned local key material and Vault state in the existing cryptographic-erasure order. It cannot erase backups, enrollment packages, Git history, external disks, or other copied artifacts.
- With the current product, copied backups remain ciphertext after hard purge unless another authorized device still retains the Vault key. Documentation must distinguish local purge from destruction of external copies.
- No recovery attempt is journaled because no post-loss recovery path exists. Ordinary backup, restore, enrollment, revocation, and purge evidence remains governed by their existing encrypted journals.

### Future user-held recovery package gate

A user-held recovery package remains disabled and is not a private-alpha requirement. Implementation requires a separate accepted ADR and product approval that satisfy all of the following gates.

- Recovery authority is generated or provisioned only while the Vault is unlocked, remains separate from backup ciphertext and ZDP identity, and is held by the user rather than silently uploaded.
- The package is Vault-bound, versioned, authenticated, encrypted, size-bounded, and contains no plaintext root key or reusable device-signing private key.
- A generated high-entropy recovery secret is the default. Accepting a human passphrase additionally requires a pinned memory-hard password-hardening algorithm, reviewed parameters, upgrade rules, minimum guidance, and an explicit offline-guessing budget.
- Recovery imports key material into the destination platform key store only after explicit confirmation. It does not silently link an account, enroll a sync device, grant capabilities, or bypass device revocation.
- Package epochs, rotation, revocation, stale-package handling, backup rollback, and concurrent recovery are defined before implementation. Offline copied material cannot honestly be described as globally single-use or globally revoked without a converged authority that proves those properties.
- Recovery records only a non-secret package identifier, epoch, result, destination device identity, and timestamps in encrypted audit state. Secrets, passphrases, unwrapped keys, and recovery ciphertext are excluded from events and logs.
- Hard-purge behavior explicitly states whether an externally held recovery package plus an externally held backup can recreate a Vault. Talos must never claim to erase artifacts it does not control.
- Native and restart tests cover wrong secrets, brute-force limits, tampering, truncation, replay, stale and revoked epochs, rollback to an older backup, partial crashes, rotation, concurrent attempts, destination key-store failure, and hard purge.

Until every gate is accepted and evidenced, `all_keys_lost` remains terminal and no renderer, CLI, or support surface may imply otherwise.

## Consequences

Talos keeps the local-encryption promise honest at the cost of possible permanent data loss. Backups protect against database, migration, and local corruption only while recovery authority survives on at least one authorized device. Account recovery remains independent from Vault decryption.

The future package boundary is explicit enough to prevent a convenience implementation from becoming hidden key escrow, while leaving room for a separately reviewed user-held design after product demand justifies the attack surface.

## Verification

- Documentation consistently describes `.talos-backup` as ciphertext without embedded recovery authority.
- Alpha readiness records total authorized-key loss as an explicit unsupported case rather than an unimplemented promise.
- Existing backup, restore, enrollment, revocation, and hard-purge contracts remain unchanged.
- This decision introduces no API, database, renderer, credential, runner, or secret-storage change.
