# Durable Permission Review Requests

- Status: Accepted
- Owner: ZDP/Talos engineering

## Context

The deterministic broker can require user review, but returning that outcome only in memory loses the request on restart and gives the renderer no authoritative object to review. Reusing a prose Decision without a typed link to the exact process intent would turn executable, arguments, environment names, timeout, and output bounds into an unsafe string convention.

## Decision

Permission review is a separate aggregate with `open`, `approved`, and `denied` states. It binds a task-scoped exact intent hash and workspace hash to the encrypted process intent.

SQLite schema version 9 adds a materialized `permission_requests` table containing identifiers, hashes, state, timestamps, and event references only. The executable path, argument vector, environment names, timeout, and output limit remain inside the private encrypted creation event.

The execution coordinator persists an idempotent request when the broker returns `require_review`. It must not create a worktree, start a worker, or prepare an execution attempt on that path.

Resolving an open request and creating its grant happen in one SQLite transaction. A one-time or task grant uses the exact task intent hash. A workspace grant uses the separately derived workspace intent hash. A denial remains task-scoped. Replaying the same resolution idempotency key returns the durable request and grant rather than creating another grant.

## Consequences

- Review requests survive app and worker restarts without leaking raw argv into materialized tables.
- Duplicate coordinator calls cannot produce duplicate review requests when they reuse the command idempotency key.
- Grant creation cannot commit if the request state transition fails.
- Wails review methods and renderer UI remain a follow-up. Expiry defaults and whether workspace-wide grants are exposed to MVP users must be decided before that transport is added.

## Verification

- Domain tests prove request hashes bind the exact intent and workspace.
- SQLite tests cover encrypted round-trip, idempotent creation, open listing, atomic resolution, replay, workspace mismatch rejection, and rollback on request-update failure.
- Coordinator tests prove review persistence happens without worktree or worker side effects.
