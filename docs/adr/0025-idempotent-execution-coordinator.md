# Idempotent Execution Coordinator

- Status: Accepted
- Owner: ZDP/Talos engineering

## Context

Process permission, one-time grant consumption, worktree ownership, worker policy installation, tool dispatch, and execution-state persistence cross several adapters. Calling those adapters directly from Wails would leak implementation details into the transport and make retries capable of dispatching the same tool twice.

The product specification requires restart-safe attempts, idempotent commands, one active run per repository, capability-based process decisions, and explicit unknown outcomes.

## Decision

The application layer owns one execution coordinator with this ordering:

1. Load the current Task and immutable current contract.
2. Evaluate the exact process intent against the contract and active grants.
3. Return review or denial without creating a worktree or worker process.
4. Atomically create the Run, dispatch-pending Attempt, tool-call identifier, and one-time-grant consumption under the caller's idempotency key.
5. If that preparation is an idempotency replay, return the materialized terminal or unknown state and never dispatch again.
6. Create an owned baseline-bound worktree and start an owned worker session.
7. Install only the evaluated capability before executing the tool.
8. Persist the Attempt result before the Run result. Transport loss or unavailable worker state becomes `unknown`, not `failed`.
9. Retain successful worktrees for review. Remove pre-dispatch failure worktrees when ownership verification permits it.

SQLite owns Run, Attempt, and tool-call identifier generation after the idempotency lookup. This prevents retries from conflicting merely because the caller generated new identifiers.

The worker adapter translates wire states and timestamps into typed application-port values. Unknown wire states or malformed timestamps are protocol failures. Raw worker errors do not become application policy.

## Consequences

- Wails can call one use case without importing Git, SQLite, worker IPC, or permission implementations.
- A repeated idempotency key cannot execute a tool twice, including after process restart.
- An ambiguous dispatch is visible and requires reconciliation instead of unsafe automatic retry.
- A completed replay does not reproduce stdout or stderr because those artifacts are not yet persisted by this slice. The replay reports durable state only.
- Permission review UI and main-process assembly remain separate follow-up work; this ADR does not authorize a generic execution transport API.

## Verification

- Coordinator tests cover review without side effects, ordered allowed execution, terminal replay without redispatch, unknown protocol outcomes, and worker-policy failure cleanup.
- SQLite tests cover generated identifiers, atomic preparation, one-time grant consumption, and idempotency replay.
- Worker adapter tests reject unknown states and malformed timestamps.
