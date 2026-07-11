# ADR 0008: Vault Bootstrap Lifecycle

- Status: Accepted
- Date: 2026-07-11
- Owners: ZDP/Talos engineering

## Context

Creating a production Vault crosses two durable stores: a DPAPI-protected key record and a per-Vault SQLite database. Treating either write as sufficient would leave an undecryptable database, an orphaned key, or a UI that reports success after only half the operation completed. Locking also needs to remove usable key material from the live process rather than only changing a status label.

## Decision

- Store Windows Vault data below `%LocalAppData%/0disoft/Talos Agent`. Protected key records and Vault databases use separate `keys` and `vaults` children.
- Generate a UUIDv7 Vault ID and a random 256-bit key-encryption key in the application use case. The renderer cannot supply IDs, paths, key identifiers, or key bytes.
- Create in this order: protect the key with current-user DPAPI, exclusively reserve and initialize the per-Vault database, then atomically append `vault.created` with its materialized state.
- If a later step fails, compensate in reverse order by closing and removing the database and deleting the protected key. Compensation ignores already-absent resources but reports an explicit incomplete-cleanup error when another cleanup failure occurs.
- Use a non-cancelled cleanup context after a caller cancellation so cancellation cannot skip local compensation.
- Hash the Vault ID into the database filename. A caller-controlled identifier never becomes a path component.
- Expose only use-case-shaped `Status`, `Create`, and `Lock` methods through Wails. Public failures use stable safe codes and do not include filesystem paths or protected-record details.
- Locking closes the SQLite store and zeroes the in-memory key copy held by its envelope sealer. Persisted metadata never records an unlocked state.

## Consequences

The first production Vault can now be created and locked without a plaintext key fallback or partial-success response. Exact cleanup is best-effort because filesystem deletion itself can fail; `VAULT_CLEANUP_INCOMPLETE` tells the caller that manual recovery is required without exposing the affected path.

The frontend now validates Wails responses and supports initial creation and locking. Opening an existing Vault, listing Vaults, retention changes after creation, hard purge, and recovery packages remain separate Phase 1 slices.

## Evidence

- application tests cover successful creation, DB-create failure, state-init failure, and failed compensation;
- adapter tests cover exclusive creation and main/WAL/SHM removal;
- transport tests cover create, duplicate-open rejection, lock, and unavailable storage;
- the Svelte control surface covers status, retention selection, creation, locking, bounded errors, and malformed-response failure;
- envelope tests prove a destroyed sealer cannot decrypt earlier ciphertext;
- standard test, frontend diagnostic, build, doctor, and scaffold checks.
