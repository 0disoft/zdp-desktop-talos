# ADR 0053: Atomic bounded folder pack exchange

- Status: Accepted
- Date: 2026-07-18

## Context

Talos could create and validate immutable encrypted packs, but callers still had to move raw bytes themselves. Jumping directly to Git would mix pack correctness with credentials, remotes, merge behavior, and push policy. A local folder transport is the smallest delivery layer that proves durable file semantics and can later sit below a renderer picker or Git adapter.

The folder is untrusted local state. Partial copies, changed bytes under the same name, symlink traversal, malformed filenames, oversized files, and concurrent publication must fail closed. Absolute Vault and device identifiers should not appear in directory names.

## Decision

- Add a `syncexchange.Exchange` port and a filesystem adapter. Application and Vault code depend on the port; direct operating-system file access remains in the adapter.
- Derive Vault, device, and pack path segments from full SHA-256 digests. Store packs under `vaults/<vault-hash>/packs/<device-hash>/<year>/<month>/` without exposing raw identifiers.
- Name files with fixed-width sequence start and end plus the pack-ID digest. Enumeration sorts numerically by sequence before import.
- Stage bytes in the destination directory, set restrictive permissions, flush the file, close it, and publish with an atomic hard link. A filesystem that cannot provide this no-clobber operation returns an error instead of falling back to an overwriting rename.
- If the destination already exists, return replay only when its bounded bytes are identical. Different bytes at the same derived identity are a conflict.
- Resolve and validate the chosen root, reject roots and descendants that are files or symbolic links, and verify every derived path remains below the resolved root.
- Read only regular `.talos-pack` entries with a strict filename grammar. Reject malformed pack names, sequence inversions, symlinks, empty files, and oversized files.
- Bound one scan to 512 packs, 16 MiB per pack, and 64 MiB total. Limits fail the operation rather than silently truncating an ordered stream.
- Keep exact pack validation, membership authorization, sequence continuity, replay, and quarantine in the existing sync importer. A filename is routing metadata, never authority.
- Leave files immutable after import. Existing encrypted receipt and replay journals are the canonical record of application; the exchange folder is disposable delivery state.

## Consequences

Two enrolled Vault installations can exchange packs through a user-controlled folder with crash-resistant publication, deterministic ordering, duplicate safety, and no Git or network side effects. A malicious same-user process can still race filesystem objects outside the guarantees of ordinary path APIs; this adapter is not an OS sandbox.

Filesystems without same-volume hard-link support are rejected. A later Git exchange operates on the already published immutable files and does not reimplement encryption, membership, or replay.

## Validation

- out-of-order writes enumerate by numeric sequence;
- identical publication replays and changed bytes conflict;
- malformed entries, file roots, canceled contexts, and size limits fail closed;
- a two-Vault enrollment E2E exports through the folder, applies on the target, and idempotently replays the same file;
- the complete Go suite passes on Windows amd64.
