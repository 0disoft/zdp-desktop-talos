# ADR 0055: Explicit clean Git pack exchange

- Status: Accepted
- Date: 2026-07-18

## Context

Atomic folder exchange proved immutable encrypted pack delivery, but using an ordinary shared folder does not bind imported bytes to a reviewed Git revision. Treating Git as a live database, automatically committing, pulling, pushing, or resolving merges would introduce credentials, hooks, remote side effects, and ambiguous recovery into the Vault runtime.

Windows adds a less obvious failure boundary: the hashed Vault/device layout plus immutable filename can exceed the legacy Git path limit even when Go can create the file. A write that the system Git client cannot see is not a valid Git exchange.

## Decision

- Implement Git delivery as a `syncexchange.Exchange` adapter around the existing folder port. Pack encryption, signing, membership, sequence validation, replay, conflict handling, and quarantine remain unchanged application contracts.
- Require an absolute canonical Git repository root with a committed branch HEAD. Reject nested selections, unborn repositories, bare repositories, detached HEAD, and a dirty index or worktree before either direction.
- Store files only beneath `talos-sync/` using the existing hashed and bounded folder layout. Do not add or change `.gitignore`, Git configuration, remotes, credentials, branches, commits, tags, merges, pulls, pushes, or hooks.
- Export one ready immutable pack only from a clean repository. Reinspect the same branch and HEAD after publication and require the sole new change to be that exact untracked pack. If the repository changes concurrently, return an ambiguous-state error and leave the immutable file for direct inspection rather than deleting user-visible state.
- A repeated export is accepted only when the identical file is already tracked in a clean repository. An ignored or otherwise untracked existing file is not proof of Git delivery.
- Import only from a clean repository. Require every enumerated pack to be tracked, then recheck the same branch, HEAD, index, and worktree before returning bytes to the authoritative importer. Clear all loaded pack bytes if the repository changes or any tracking check fails.
- Enumerate every untracked file instead of collapsing an untracked directory into one status row. Bound the result with the existing repository change limit.
- Pass `core.longpaths=true` to each Talos-owned Git process invocation on Windows-compatible clients. Do not mutate the user's local or global Git configuration.
- Expose separate renderer actions for Git preparation and committed-pack import. Tell the user that Talos never commits, pulls, or pushes; those remain explicit external actions.

## Consequences

Git is an auditable immutable delivery carrier, not Talos state authority. A user prepares a pack, reviews and commits it with their normal Git tooling, transfers it by their chosen remote or offline process, and imports only after the receiving worktree is clean at that commit.

Talos does not prove remote publication, remote access control, branch protection, or deletion from Git history. A failed post-write check may leave one uncommitted encrypted pack in `talos-sync/`; the UI reports that the repository changed and requires direct inspection.

## Validation

- clean branch export produces exactly one untracked `talos-sync/` pack and performs no Git write command;
- dirty, detached, nested-root, ignored-pack, and concurrent-HEAD cases fail closed;
- committed identical export replays while untracked or ignored bytes are rejected;
- import accepts tracked clean packs, rejects dirty repositories, and clears bytes after a revision race;
- Windows integration covers the full hashed path with per-invocation long-path support;
- two enrolled independent Vaults prove uncommitted import rejection, manual commit, committed import, workspace-remap requirement, and idempotent replay;
- the complete Go suite, Svelte diagnostics, frontend production build, binaries, doctor, and scaffold checks pass.
