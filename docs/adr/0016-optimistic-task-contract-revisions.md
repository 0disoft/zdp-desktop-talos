# ADR 0016: Optimistic Task Contract revisions

- Status: Accepted
- Date: 2026-07-11

## Context

Task Contracts must evolve without overwriting prior decisions. Two windows, retries, or future devices may attempt to revise the same current contract, and last-write-wins would silently discard scope or safety changes. Replaying an older idempotent command after newer revisions also must return the original command result instead of pretending the old event is still the Task's current event.

## Decision

- Append each revision as a new encrypted `task.contract.revised` event and immutable revision pointer.
- Require `expected_revision`; update the Task head only when it still matches that revision.
- Keep the original workspace root and baseline commit across all revisions. Reinspect the current clean workspace and compare the stored Vault, root, and baseline before revision.
- Bind idempotency to Task ID, expected revision, contract content, and risk. Generated occurrence time is not part of the caller intent.
- Reconstruct idempotent results from the referenced historical event and revision pointer, even when the Task head has advanced further.
- Surface stale updates as `TASK_REVISION_CONFLICT`; do not merge contract scope automatically.

## Consequences

Concurrent revision attempts have one winner and one explicit conflict. Historical revisions remain decryptable and auditable, while the Task row points to the latest accepted revision. The desktop can list up to 50 recent contracts for the unlocked Vault and the active mapped workspace and baseline, including after restart. Selecting one restores the current contract without executing commands or applying patches. Later actions still enforce current revision, workspace, and review gates; contract editing preserves additional verification commands and existing forbidden actions.

## Verification

Task discovery also supports status filtering before the page limit, stable creation-time/ID cursors, and goal matching over each bounded 50-record encrypted page. Empty search pages may have a continuation cursor; no plaintext goal index is created. Other baselines can be inspected read-only within the same active workspace mapping. The legacy list operation retains its existing latest-update ordering and baseline scope.

- sequential revision 1 to 2 and current-head reads;
- stale revision rejection without extra pointer rows;
- concurrent same-revision updates produce one success and one conflict;
- create and revision idempotency replay after the head advances;
- Wails workspace/Vault binding and Svelte revision flow.
