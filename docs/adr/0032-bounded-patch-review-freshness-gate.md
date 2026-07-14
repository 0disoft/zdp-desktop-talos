# ADR 0032: Bounded Patch Review Freshness Gate

- Status: Accepted
- Date: 2026-07-14

## Context

Successful verification evidence is useful only while it still describes the retained task worktree and the current Task Contract revision. The desktop previously displayed the evidence returned by one execution call but could not reload the authoritative proof after restart or detect a later patch or contract change.

Sending an unrestricted Git diff to the renderer would create a second data-egress surface before redaction, secret scanning, and response-size limits exist. Patch review still needs enough information to identify the affected files without exposing absolute local paths or raw source content.

## Decision

- Query the newest successful verification evidence by Vault and Task, ordered by finish time and evidence identity.
- Reopen only the marker-verified Talos-owned task worktree. Patch Review never inspects an arbitrary supplied path.
- Compute the deterministic worktree state hash before and after bounded Git status collection. If the hash changes during inspection, fail closed rather than return a torn review.
- Return repository-relative paths, rename origins, change kinds, index/worktree status, the current state hash, and bounded evidence metadata. Do not return the owned worktree path, executable, environment, stdout, stderr, or raw diff content.
- Classify the result as `fresh` only when baseline commit, current contract revision, and worktree state hash all match the newest evidence. Classify mismatches as `stale` with a stable reason code and missing evidence as `unverified`.
- Treat this service as read-only. Apply, discard, and task completion remain separate explicit commands.

## Consequences

The renderer can reload a review after application restart and clearly withdraw the verified state after any patch or contract change. The double snapshot adds local hashing cost, but avoids presenting a status list from one filesystem state beside a hash from another.

Users see changed files but not line-level content yet. A later raw-diff viewer must add redaction, secret scanning, binary detection, per-file and total payload limits, and safe rendering before crossing the Wails boundary.

## Verification

- application tests cover fresh, changed-patch, missing-evidence, and worktree-ownership failure paths;
- Git adapter tests continue to bound status parsing and deterministic state snapshots;
- Go tests compile every Vault database implementation against the new evidence query;
- Svelte checking validates the strict Patch Review response parser and UI states.
