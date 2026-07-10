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

## Decisions

A Decision blocks only named steps or capabilities. Safe unrelated steps continue. Answers carry the question revision and expected repository revision. Concurrent incompatible answers become `conflicted`; last-write-wins is forbidden for security, privacy, license, public API, and deletion choices.

## Failure and Recovery

The control plane journals an attempt before worker execution. A worker crash yields an unknown or failed attempt, not implicit success. Recovery reconciles attempt identity and idempotency keys before retrying. Model/provider failure cannot bypass permission or verification gates. Disk-full, locked database, corrupt IPC, and stale repository state fail closed with actionable state.

## Completion

Verification evidence records the repository revision, diff hash, command identity, exit status, bounded output reference, and timestamp. Any later patch change invalidates affected evidence before completion is reevaluated.
