# ADR 0062: Durable Task Execution Budgets

- Status: Accepted
- Date: 2026-08-15

## Context

Per-request byte and timeout limits bound one model call or tool execution, but they do not stop a Task from accumulating repeated calls after retries, process restarts, or concurrent requests. In-memory counters would reset with the application and checking a limit after an external call would be too late to prevent the side effect.

Provider prices are mutable external data. A monetary ceiling without a trusted, versioned price snapshot would report false precision and could either overrun or incorrectly block a Task.

## Decision

- Schema 25 stores one durable counter per Task for the policy snapshot, model calls, tool calls, input tokens, output tokens, outstanding token reservations, start time, and update time. Later runtime or application defaults cannot silently reinterpret an existing Task budget.
- Model and tool capacity is reserved in the same SQLite transaction that records the durable prepared receipt or attempt. An exhausted budget fails before provider, worktree, or worker side effects.
- Model reservations include the complete serialized request byte count as a conservative input-token ceiling and the explicit provider output-token limit. Completion atomically replaces the reservation with provider-reported usage.
- A provider response that exceeds its reservation is recorded accurately and then returns a budget-exhausted result. Future calls remain blocked; actual usage is never discarded to make a limit appear satisfied.
- Idempotent retries reuse their existing receipt or attempt and do not consume capacity twice.
- The first bounded Task operation starts a fixed wall-clock window. Restarting the application does not reset it.
- The current default permits four model calls, sixteen tool calls, 128 Ki input-token ceiling, 16 Ki output-token ceiling, and thirty minutes.
- Monetary cost is not estimated until a trusted versioned price source and model identity contract exist. Call, token, and time limits remain the enforceable cost controls meanwhile.

## Consequences

The runtime now has a fail-closed Task-wide ceiling across restarts and concurrent calls. Reservations can remain after an interrupted provider call; this intentionally favors preventing duplicate or unbounded external usage over automatically reclaiming uncertain capacity. A later recovery design may reconcile a reservation only with provider-owned idempotency evidence.

Byte count can overestimate input tokens but cannot underestimate them for ordinary UTF-8 provider payloads. This conservatism reduces usable context before it risks an overrun.

## Verification

- policy validation rejects zero, negative, and unreasonable limits;
- concurrent and distinct reservations cannot exceed one Task budget;
- idempotent receipt and attempt retries consume a budget only once;
- budget exhaustion stops before provider and worker execution;
- schema 0 through 24 databases migrate to schema 25 with reservation columns, durable counters, and the Vault/update index;
- complete and failed model receipts clear their reservations while preserving measured usage;
- a live provider response without positive input and output usage is rejected instead of releasing reservations against a fabricated zero.
