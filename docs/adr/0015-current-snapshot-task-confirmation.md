# ADR 0015: Current-snapshot Task confirmation

- Status: Accepted
- Date: 2026-07-11

## Context

The renderer can display a repository snapshot for an arbitrary amount of time before the user confirms a Task Contract. Persisting that stale UI snapshot would bind the Task to a baseline that no longer describes the selected worktree. A dirty worktree also contains content that is not represented by the commit baseline.

## Decision

- Expose a use-case-specific `TaskService.CreateContract` Wails method rather than generic database or filesystem access.
- Require an unlocked Vault and an open Workspace.
- Reinspect the canonical repository root immediately before persistence. Reject a changed commit baseline and reject any dirty worktree.
- Pass the reinspected root and exact commit to the Vault session; the renderer cannot choose or override the persisted baseline.
- Use a request ID bound to the complete contract intent for idempotent retries.
- Return only Task identity, revision, baseline, risk, status, and creation time to the renderer.

## Consequences

The first confirmed contract is restart-safe and cannot be created from a stale or dirty UI snapshot. Git state cannot be locked transactionally with SQLite; later execution must still create its worktree from the recorded commit rather than assume the primary worktree has remained unchanged. Revision 2 and later contract editing remains a separate Phase 2 slice.

## Verification

- Wails tests prove clean baseline reinspection reaches persistence.
- Dirty and changed baseline fixtures fail before the Task store is called.
- Frontend response parsing rejects malformed Task results.
- Svelte diagnostics and production build cover the confirmation form.
