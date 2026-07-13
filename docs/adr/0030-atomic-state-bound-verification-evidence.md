# ADR 0030: Atomic State-Bound Verification Evidence

- Status: Accepted
- Date: 2026-07-13

## Context

A successful process exit is not durable verification by itself. Without a fingerprint of the exact task worktree, the result can be shown after files, staging state, or untracked inputs have changed. Writing evidence after marking an attempt successful creates a crash window where the execution journal claims success but no proof exists.

Raw stdout and stderr are also unsafe evidence inputs before redaction and secret scanning exist. Test output can contain tokens, customer data, private paths, or source excerpts even when the command itself is approved.

## Decision

- Add SQLite schema version 10 with a `STRICT` `verification_evidence` table. Each evidence row belongs to one Vault, Task, Run, and successful Attempt and records the immutable contract revision, command index, baseline commit, capability hash, worktree state hash, zero exit code, execution times, and encrypted event provenance.
- Insert the successful Attempt transition, encrypted finish event, idempotency claim, and verification evidence in one SQLite transaction. A successful Attempt without evidence is invalid. Failed, canceled, or unknown Attempts cannot carry evidence.
- Compute `talos.worktree-state/1` from the baseline commit plus deterministic Git index metadata and current content hashes for every tracked and non-ignored untracked path. Deleted files, staged content, working-tree content, file mode, and symbolic-link target text affect the hash.
- Snapshot the owned task worktree only after the worker returns a known zero exit status. Snapshot failure converts the Attempt and Run to failed with a safe code; it never falls back to evidence-free success.
- Replayed successful Attempts must load their original evidence. Missing or corrupt evidence makes replay fail closed.
- Expose only the evidence ID, contract revision, command index, and worktree state hash through the bounded Wails result. Do not expose executable paths, environment values, or raw process output.
- Do not persist stdout or stderr until a bounded redaction and secret-scanning pipeline owns that payload. Exit status and state binding are the current evidence contract.

## Consequences

The UI can distinguish a fresh execution from replayed proof and can name the exact state fingerprint that passed. A later patch-review gate can recompute the same worktree hash and mark evidence stale before completion or apply.

Hashing reads repository files and is intentionally bounded by Git command output and entry-count limits. Very large repositories may fail verification rather than receive incomplete evidence. Worktree ownership and no-follow handling reduce accidental path escape, but same-user filesystem races and platform sandboxing remain separate hardening work.

## Verification

- worktree snapshot tests prove tracked and untracked content changes alter the hash;
- domain tests reject missing state hashes and nonzero evidence;
- SQLite tests prove successful evidence persistence, terminal replay, and rollback when evidence insertion fails;
- coordinator tests prove snapshot failure becomes a failed run with no evidence;
- Wails tests prove only bounded state-binding metadata crosses the renderer boundary.
