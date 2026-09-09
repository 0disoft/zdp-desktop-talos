# ADR 0034: Atomic Patch Actions and Completion Gate

- Status: Accepted
- Date: 2026-07-15

## Context

Patch Review can prove that a bounded diff still matches successful verification evidence, but reading that proof does not safely mutate the primary worktree. Applying or discarding a patch crosses a durable database boundary and an external Git filesystem boundary that cannot share one transaction. A crash between those boundaries must not cause an automatic second apply or delete an unverified directory during retry.

Task completion also cannot be a renderer choice. It must follow the current immutable contract, fresh evidence, changed-path scope, blocking Decision state, secret scan, and primary-worktree baseline.

## Decision

- Treat apply and discard as explicit idempotent commands with a durable `patch_actions` journal.
- Resolve an existing caller key after Vault/workspace identity checks but before fresh-review checks. Compare Task, action kind, revision, and patch hash; replay the recorded result even after completion or worktree removal, and reject changed intent under the same key.
- Persist `pending` before the Git mutation. Finish as `succeeded`, `failed`, or `unknown`; a replay of `pending` or `unknown` never repeats the external mutation automatically.
- Bind every action to Task ID, contract revision, patch hash, worktree state hash, and, for apply, the exact verification evidence ID.
- Require apply to pass the deterministic Completion Gate: current contracted Task, exact revision and patch hash, fresh evidence, zero secret findings, every changed path and rename origin inside allowed paths, and no unresolved blocking Decision.
- Reinspect the canonical primary worktree immediately before apply. Its HEAD must equal the immutable baseline and its tracked and untracked status must be clean. Revalidate the expected state hash directly instead of rebuilding renderer diff previews; bracket binary-patch construction with state checks so a concurrent task-worktree change fails closed.
- Build a binary patch from the owned task worktree using a temporary alternate Git index. Apply it to the primary worktree only after `git apply --check`; never commit, push, or move primary HEAD.
- Permit discard without fresh evidence, because removing a rejected patch must remain possible, but require the current patch hash and marker-verified owned worktree before removal.
- Commit successful action state and the terminal Task outcome (`completed` or `discarded`) in one SQLite transaction. Keep failed or unknown Tasks contracted for explicit recovery.

## Consequences

The primary worktree can become dirty only after an explicit apply. A crash after an external mutation may leave a journal entry unresolved, but Talos reports the uncertainty instead of guessing or applying twice. The owned worktree is retained after apply for review and recovery; discard removes it.

The application-level envelope encryption protects event payloads, while materialized action metadata contains only identifiers, hashes, state, safe error codes, and timestamps. Raw diff text and absolute paths are not written to the action journal.

## Verification

- pure gate tests cover stale evidence, secret findings, scope escape, and discard semantics;
- SQLite integration tests cover prepare replay, successful terminal transition, and atomic Task outcome projection;
- Git integration tests cover tracked and untracked apply, unchanged primary HEAD, dirty-primary conflict, stale hash rejection, and owned-worktree discard;
- renderer diagnostics validate strict command responses, terminal Task states, explicit apply, and confirmed discard controls.
