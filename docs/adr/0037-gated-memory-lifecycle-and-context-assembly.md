# ADR 0037: Gated memory lifecycle and bounded context assembly

- Status: Accepted
- Date: 2026-07-17

## Context

An encrypted event ledger is not a memory system. Replaying raw task events into a prompt would preserve noise, stale decisions, rejected suggestions, and prompt injection as if they were durable instructions. A model-generated sentence also cannot promote itself into long-lived authority. Talos needs a reviewable lifecycle that keeps provenance, scope, sensitivity, and applicability attached to every remembered statement and can explain why a later plan saw it.

## Decision

- Represent durable memory as six bounded kinds: preference, constraint, decision, procedure, failure pattern, and environment fact.
- Create only `candidate` records from extraction. A candidate carries a non-empty statement and rationale, normalized applicability terms, Vault or workspace scope, sensitivity, confidence, source actor, and one or more existing encrypted event IDs from the same Vault.
- Require an explicit optimistic-revision transition before a candidate becomes `approved`, `rejected`, or `quarantined`. Stable promotion and later stale, deprecated, superseded, or reapproved transitions remain separate commands. Rejected, quarantined, deprecated, and superseded records are terminal.
- Persist the complete memory snapshot and transition reason only in encrypted event payloads. The schema-14 materialized table stores lifecycle, scope hash, sensitivity, confidence, revisions, timestamps, and event pointers; it never stores the statement, rationale, applicability terms, or raw provenance content.
- Select only approved and stable records for model context. Context Assembly first narrows by Vault and workspace scope, then by normalized task-goal or allowed-path terms, and finally by product-owned item and byte budgets. Stable records sort ahead of approved records; confidence and update time break ties deterministically.
- Return the selected memory ID, revision, source reference, and local applicability reason with the plan result. Only the bounded statement crosses the model boundary. Rationale and evidence payloads stay local.
- Mark every selected memory block as `untrusted_data`. Planning prompt `planning.v2` permits approved memory to guide a plan but forbids it from altering the Task Contract or granting permission. The existing plan validator, Permission Broker, execution journal, and Verification Gate remain authoritative.
- Fail before model egress when configured memory assembly fails. Silent memory omission would make planning behavior unexplainable.

## Consequences

Approved memory can change a later plan without becoming execution authority. Rejected or stale knowledge cannot leak through the active-context query, and cross-Vault evidence cannot manufacture provenance. Context selection stays deterministic and bounded without a vector index. ADR 0038 adds deterministic answered-Decision extraction and the explicit review/explanation surface; broader extraction quality, automatic stable-promotion policy, projection, sync conflict handling, and deletion UX remain later work.

## Verification

- domain tests cover kinds, normalization, review timestamps, secret rejection, and closed terminal transitions;
- SQLite tests cover same-Vault evidence, encrypted statement storage, idempotent candidate creation, optimistic transitions, workspace filtering, and stale-revision rejection;
- application tests cover explicit review outcomes, applicability filtering, item and byte budgets, provenance reasons, and untrusted model framing;
- the encrypted integration scenario proves candidate creation, explicit approval, Context Assembly, `planning.v2`, and a later fixture plan changed by the selected statement.
