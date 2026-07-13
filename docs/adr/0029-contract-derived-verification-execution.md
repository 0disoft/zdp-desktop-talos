# ADR 0029: Contract-Derived Verification Execution

- Status: Accepted
- Date: 2026-07-13

## Context

A stored verification command is a request, not execution authority. If the renderer supplies an executable, environment, timeout, output limit, or a complete process intent, it can bypass trusted product policy. Resolving a command before loading the current contract also creates a race where an older revision's command can run after the contract changes.

The existing execution coordinator already owns permission evaluation, durable attempts, task worktrees, worker policy, cancellation classification, and terminal replay. It needs a caller-facing operation that preserves those guarantees without exposing those internal steps through Wails.

## Decision

- `ExecutionService.ExecuteVerification` accepts only Task ID, zero-based contract command index, request ID, and correlation ID.
- The coordinator reloads the current Task and immutable current contract revision before resolving the indexed command. Invalid or stale indexes fail before permission, worktree, journal, or worker effects.
- The Permission Broker also acts as the trusted rule resolver. It maps a contract rule ID and exact argv to an absolute executable and server-owned timeout and output limit, then reevaluates the resulting exact intent against grants and contract forbiddance.
- The initial bootstrap catalog contains only `go-test`, resolved through the installed system Go executable, with a `test` prefix, at most 64 arguments, a ten-minute timeout, a 1 MiB output limit, no inherited environment values, and default user review.
- Bootstrap constructs the broker, owned worktree manager, and sibling worker factory. Domain and application code do not construct Git, process, Wails, or SQLite implementations.
- The Wails result exposes only review-required or successful state, durable identifiers, replay status, and successful exit code. Worker stdout, stderr, executable paths, environment values, and internal errors do not cross this surface.
- Vault locking and hard purge serialize behind an active verification call so the database and Vault session cannot close during journaling. Cancellation and background-run UX remain a later bounded change.

## Consequences

The renderer cannot broaden execution even if it is compromised. Permission review displays the exact server-resolved intent, and approval remains capability-hash scoped. A contract revision changes the command lookup source immediately because execution always reloads the current revision.

Holding the Vault service mutex keeps lifecycle ownership simple but means lock, retention, purge, and other Vault calls wait for a synchronous verification. The current rule timeout bounds that wait; a future cancellable job API must replace this only with durable run ownership and explicit shutdown semantics.

## Verification

- Permission Broker tests cover trusted resolution, unknown rules, prefix mismatch, argument broadening, and argument-copy isolation.
- Coordinator tests prove an unknown command index causes no permission, worktree, journal, or worker side effect.
- Wails tests prove bounded review state, exact idempotency namespace, locked-Vault rejection, and safe unavailable-runtime errors.
- Full Go tests, Svelte diagnostics, frontend build, binary build, and worker doctor cover the assembled desktop path.
