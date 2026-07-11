# ADR 0020: Owned task worktree lifecycle

- Status: Accepted
- Date: 2026-07-11

## Context

Task execution must never mutate the user's primary worktree. A raw destination path or ordinary `git checkout` is not enough: checkout may consult executable filters or hooks, failed materialization may leave registered worktrees behind, and broad cleanup can delete user-owned data.

## Decision

- Create detached task worktrees only from the Task Contract's full baseline commit through the repository worktree port.
- Place task worktrees, empty hook policy, and ownership markers under one application-owned local-data root. Task IDs select fixed child names; arbitrary destination paths are not accepted.
- Disable system and global Git configuration, credential prompting, optional locks, fsmonitor, untracked cache, color, hooks, and pagers for lifecycle commands.
- Reject repositories whose local Git configuration declares executable clean, smudge, or process filters before checkout.
- Register the worktree without checkout, then materialize the exact baseline with hooks disabled and per-invocation Windows long-path support.
- Remove only a worktree whose record and bounded owner marker agree on task, primary root, worktree root, baseline, and schema. A missing or modified marker fails closed.
- On failed creation, compensate only the exact child beneath the application-owned worktree root. Do not clean an ambiguous parent or user-selected directory.

## Consequences

The primary worktree may remain dirty while a task receives a clean detached view of its recorded baseline. Worktree isolation is a write-location policy, not an OS sandbox; code executed inside it may still access the user's account until platform sandboxing is added. Same-user races involving junctions or reparse points are not claimed to be impossible and remain a hardening target.

## Verification

- dirty primary HEAD and status remain byte-for-byte unchanged across create, task modification, and forced removal;
- the task worktree starts clean and detached at the exact baseline;
- executable checkout filters are rejected;
- a tampered owner marker prevents removal;
- domain path and provenance invariants reject a primary/task root collision.
