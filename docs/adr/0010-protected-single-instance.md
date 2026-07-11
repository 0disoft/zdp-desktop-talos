# ADR 0010: Protected Single-Instance Desktop Runtime

- Status: Accepted
- Date: 2026-07-11
- Owners: ZDP/Talos engineering

## Context

The protected Vault catalog is a read-modify-write DPAPI record with process-local synchronization. Two Talos desktop processes could otherwise read the same catalog revision and overwrite one another even though each individual protected-record replacement is atomic.

Wails provides a cross-platform single-instance lock, but its second-instance notification includes process arguments and working directory. Enabling it with a zero key would transmit that local metadata through unencrypted IPC, while embedding one shared key in the binary would provide no per-user separation.

## Decision

- Run one Talos desktop instance per operating-system user profile through Wails `SingleInstanceOptions` with the stable ID `com.0disoft.talos-agent`.
- Generate one random 256-bit instance IPC key and store it as a current-user DPAPI record under the Talos system namespace. Concurrent first-launch creation resolves `already exists` by loading the winning protected key.
- Configure single-instance IPC only after the protected key is successfully loaded. If instance-key setup fails, disable Vault storage rather than permit catalog writes without the process lock.
- Encrypt Wails second-instance notifications with the protected instance key. The first process ignores arguments, working directory, and additional data; it only restores and focuses the existing window.
- Never log or persist second-instance notification content.

## Consequences

Normal desktop writes now have one process owner, which closes the catalog lost-update gap for the supported runtime. The lock is an application-instance boundary, not a sandbox or defense against malware running as the same user.

CLI commands and future helper processes must not mutate the Vault catalog directly. Any later background service needs its own explicit IPC ownership contract rather than bypassing this single-writer boundary.

## Evidence

- protected instance-key create/reload and plaintext-marker test;
- concurrent key creation fallback in the bootstrap implementation;
- desktop compilation against the pinned Wails single-instance API;
- Vault storage fail-closed assembly when the instance key is unavailable.
