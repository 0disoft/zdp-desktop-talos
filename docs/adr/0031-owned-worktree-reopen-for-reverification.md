# ADR 0031: Owned Worktree Reopen for Reverification

- Status: Accepted
- Date: 2026-07-13

## Context

Talos intentionally keeps a successful task worktree for patch review. Creating a fresh worktree for every verification therefore makes the second verification fail with an existing-path error, while deleting the first worktree would discard the patch being reviewed. Reusing an arbitrary existing directory is worse because a stale or replaced path could redirect trusted execution outside Talos-owned state.

## Decision

- When creation reports that the task worktree already exists, the coordinator asks the worktree adapter to reopen it. Other creation failures remain terminal.
- Reopen requires the protected owner marker to match the Task ID, canonical primary repository, exact owned worktree path, and baseline commit.
- The adapter canonicalizes the existing directory, proves it remains inside the Talos-owned worktree root, verifies Git reports that exact top level, and verifies `HEAD` still equals the immutable Task baseline. Working-tree modifications are expected and retained for review.
- A reopened worktree is never removed as compensation for a later worker startup failure. Compensation removes only a worktree created by the current execution call.
- Marker mismatch, path replacement, missing Git registration, or baseline drift fails closed as an ownership error. Talos does not repair or delete an unverified directory automatically.

## Consequences

The same Task patch can be tested repeatedly and after an application restart without touching the primary worktree. A different Task revision still uses the same immutable baseline; contract revision changes alter the verification evidence rather than silently rebasing the patch.

Patch apply/discard and stale-evidence comparison remain separate review operations. Platform sandboxing and same-user filesystem races are not solved by marker verification.

## Verification

- Git adapter tests reopen a modified owned worktree and reject a mismatched baseline;
- coordinator tests prove a new verification reopens instead of recreating the retained worktree;
- compensation tests continue to remove only newly created worktrees when worker policy startup fails.
