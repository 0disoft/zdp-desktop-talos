# ADR 0058: Signed update preparation is separate from installer execution

- Status: Accepted
- Date: 2026-07-19

## Context

Manual Vault backup and restore prove data recovery, while the Windows packaging pipeline proves how a signed installer is produced and checked on a provisioned host. Neither proof authorizes the desktop to install a selected file. An update must bind publisher identity, release channel, target platform, version applicability, immutable installer bytes, the current Vault revision, and a successfully preflighted encrypted backup before any installer process can start.

The production manifest public key has not been provisioned. Treating a key from a manifest, environment variable, command argument, or renderer request as trusted would reduce signature verification to theater.

## Decision

- Define `talos.update-manifest/1` as a strict JSON Schema contract. The detached Ed25519 signature covers the exact manifest bytes.
- Pin publisher keys in reviewed application assembly. The verifier accepts a public key dependency but never discovers trust from the update payload or user input.
- Verify a local regular manifest, detached signature, and installer file before creating a backup. Reject links, unknown manifest fields, expired or future manifests, wrong channels, wrong OS or architecture, inapplicable versions, artifacts larger than 2 GiB, and size or SHA-256 mismatches.
- Create and preflight an encrypted Vault backup only after release verification succeeds. Recheck the Vault revision after preflight.
- Persist a path-free `talos.update-preparation/1` record in the current-user protected key store. It binds the release and signing-key hashes to the exact Vault revision and backup identity/hash. User-selected source paths never enter the protected record.
- Invalidate the protected preparation before a staged live restore changes generations, and delete it during hard purge.
- Before installer execution, verify the manifest and installer again, reload the protected preparation, preflight the selected backup again, and require every bound field to match. Missing, corrupt, expired, or stale evidence fails closed.
- Keep installer launch, network download, restart handoff, and automatic rollback disabled until a production publisher key is provisioned and the native N-1 evidence gate passes.

## Consequences

The repository now owns a complete local preparation and authorization core, but the desktop still has no automatic updater or installer-launch surface. A verified preparation is evidence, not permission to execute an installer. A failed protected-journal write may leave an encrypted backup file, but it never leaves an install authorization or mutates the live Vault.

The artifact is hashed once during preparation and again during authorization. A future installer adapter must consume an already-authorized immutable handle or repeat verification immediately before execution; a path string alone is not proof that the file stayed unchanged.

## Validation

- raw-byte signature verification and signing-key fingerprinting;
- strict schema and unknown-field rejection;
- channel, target, expiry, minimum-current-version, and target-version rejection;
- artifact name, regular-file, size, and SHA-256 binding;
- release verification before backup creation;
- backup receipt and isolated preflight equality;
- protected preparation round trip, replacement, deletion, and corrupt-record rejection;
- missing, expired, changed-Vault, and mismatched authorization failures.
