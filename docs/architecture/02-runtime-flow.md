# Runtime Flow

- Status: Accepted baseline

```text
high-level task
  -> immutable Task Contract revision
  -> repository snapshot + approved scoped memories
  -> redaction and model egress receipt
  -> structured plan and Tool Intents
  -> deterministic capability decision
  -> worker execution in task worktree
  -> artifacts and current-diff verification
  -> patch apply/discard decision
  -> memory candidates with provenance
  -> Memory Gate
  -> later Context Pack
```

The desktop confirms a first contract only after reinspecting the canonical repository root. A dirty worktree or changed commit baseline fails before Vault persistence; later execution uses the recorded commit rather than trusting the primary worktree to remain unchanged.

## Decisions

A Decision blocks only named steps or capabilities. Safe unrelated steps continue. Answers carry the question revision and expected repository revision. Concurrent incompatible answers become `conflicted`; last-write-wins is forbidden for security, privacy, license, public API, and deletion choices.

## Failure and Recovery

The control plane journals an attempt before worker execution. A worker crash yields an unknown or failed attempt, not implicit success. Recovery reconciles attempt identity and idempotency keys before retrying. Model/provider failure cannot bypass permission or verification gates. Disk-full, locked database, corrupt IPC, and stale repository state fail closed with actionable state.

## Completion

Verification evidence records the contract revision, command index, baseline commit, capability hash, deterministic task-worktree state hash, zero exit status, and execution timestamps. The successful Attempt transition and evidence row commit atomically. Raw stdout and stderr are not persisted until redaction and secret scanning own that path. Any later patch change produces a different state hash and invalidates affected evidence before completion is reevaluated.

The desktop execution surface accepts only a Task ID and verification-command index. The coordinator reloads the current Task and contract revision, resolves the stored rule through bootstrap-owned tool policy, and only then evaluates permission and dispatches an exact worker capability. Unknown rules, stale indexes, unavailable tools, and mismatched policy fail before worktree or worker side effects.

The first execution creates a detached Talos-owned task worktree. Later verification reopens that retained patch only after the owner marker, canonical paths, Git top level, and immutable baseline HEAD all match. A directory that cannot prove ownership is neither executed nor deleted automatically.

Patch Review reopens the same owned worktree and returns only a bounded Git status summary: repository-relative paths, change kinds, index/worktree status, and the deterministic current-state hash. It compares the newest successful evidence with the current Task contract revision, immutable baseline, and current-state hash. Any mismatch is `stale`; missing evidence is `unverified`. Raw diff content remains outside the renderer boundary until redaction and payload limits own that path.

The Safe Diff surface reads tracked changes through system Git with external diff and text-conversion hooks disabled, and reads untracked regular files without following symlinks. It computes the status, bounded diff set, and final state snapshot in one fail-closed review operation. Binary, symbolic-link, unsupported, excess-file, per-file, and total-payload cases return metadata rather than unrestricted content. A deterministic scanner redacts high-confidence credential and private-key shapes before text crosses the Wails boundary; scanner failure aborts the review.
