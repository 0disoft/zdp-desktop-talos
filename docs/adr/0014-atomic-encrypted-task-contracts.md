# ADR 0014: Atomic encrypted Task Contracts

- Status: Accepted
- Date: 2026-07-11

## Context

A Task without its first contract revision is not executable state, and a contract without its Task has no lifecycle owner. Separate transactions could leave an unusable half-record after a crash. Contract goals, path scopes, forbidden actions, and acceptance criteria may reveal private repository intent and must not be copied into plaintext materialized rows merely for convenient queries.

## Decision

- Create the Task, immutable revision 1 pointer, encrypted `task.contract.created` event, and idempotency claim in one SQLite transaction.
- Store only a SHA-256 workspace-root identifier and baseline commit on the Task. Keep the actual local path in the encrypted event. Repeat the baseline on each contract pointer and enforce a composite foreign key so a revision cannot silently move to another baseline.
- Keep contract body fields only in the encrypted event payload. The revision table stores Task ID, revision, baseline, creation time, and event provenance.
- Bind idempotency to the complete creation intent. Reusing a key with changed goal, scope, baseline, risk, or criteria fails closed.
- Require an active Vault and repository-relative, unique allowed paths before persistence.

## Consequences

Task listing can use bounded materialized metadata without decrypting contract bodies. Reading a contract decrypts and validates its provenance event. Schema version 5 is forward-only and older binaries must reject it.

## Verification

- Task, contract, event, and idempotency rollback together on a forced contract insert failure.
- Same-intent replay returns the original identities; changed-intent replay is rejected.
- A composite foreign key rejects baseline mutation.
- Restart restores the same Task and contract through encrypted event decryption.
- Contract goal markers are absent from checkpointed database bytes.
