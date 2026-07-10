# ADR 0004: Windows DPAPI Key Store

- Status: Accepted
- Date: 2026-07-10
- Owners: ZDP/Talos engineering

## Context

Envelope-encrypted events are only as safe as the Vault key-encryption key. A plaintext key file would turn encryption into decoration. Talos also needs deterministic failure behavior on platforms where a secure adapter does not exist, and key rotation must not create a window where the only valid key file is missing.

## Decision

- On Windows, protect Vault key-encryption keys with DPAPI in current-user scope. Do not use `CRYPTPROTECT_LOCAL_MACHINE` because every account on the device would share the machine protection boundary.
- Use the already locked official `golang.org/x/sys/windows` package and promote it to a direct dependency. Do not add another credential-storage wrapper.
- Bind the Vault ID and key ID into SHA-256-derived DPAPI optional entropy. Hash that same canonical reference for the filename so raw identifiers do not become storage paths.
- Restrict reference characters and lengths before path derivation. Reject empty and oversized secrets, cap protected-record reads, and distinguish invalid, missing, duplicate, corrupt, unsealable, and unsupported states with typed sentinel errors.
- Write protected records through same-directory temporary files. New records use a no-replace move; rotation uses Windows `ReplaceFileW` so the existing protected record is atomically replaced.
- Keep plaintext values in memory only for the requested operation. Never print secrets or protected blobs in doctor output.
- Non-Windows builds return an explicit unsupported error. No plaintext fallback is allowed.

## Consequences

The DPAPI blob is bound to the current Windows user profile and the Talos Vault/key reference. Copying it to another device, Windows user, Vault, or key reference will not make it usable. Loss or corruption of the Windows profile can therefore make the Vault key unrecoverable; backup and recovery need a separately designed enrollment or recovery package rather than weakening local protection.

DPAPI protects confidentiality and integrity but does not stop the same logged-in user or malware running as that user from deleting protected files or asking DPAPI to decrypt them. OS process sandboxing, malware resistance, signed packaging, backup, and account recovery are separate controls.

## Evidence

- DPAPI put, duplicate rejection, reopen, get, rotate, delete, and canceled-context tests
- path-traversal reference rejection
- plaintext-marker absence and moved-record reference-binding tests
- doctor DPAPI lifecycle probe
