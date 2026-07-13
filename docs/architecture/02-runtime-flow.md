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

Verification evidence records the repository revision, diff hash, command identity, exit status, bounded output reference, and timestamp. Any later patch change invalidates affected evidence before completion is reevaluated.

The desktop execution surface accepts only a Task ID and verification-command index. The coordinator reloads the current Task and contract revision, resolves the stored rule through bootstrap-owned tool policy, and only then evaluates permission and dispatches an exact worker capability. Unknown rules, stale indexes, unavailable tools, and mismatched policy fail before worktree or worker side effects.
