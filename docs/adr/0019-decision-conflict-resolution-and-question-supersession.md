# ADR 0019: Decision conflict resolution and question supersession

- Status: Accepted
- Date: 2026-07-11

## Context

Preserving incompatible answers is only half of a Decision Queue. A user must be able to select one existing answer without erasing the alternatives, and a runtime must be able to replace an obsolete question without letting answers from the old revision satisfy the new question.

## Decision

- Resolve a conflict by appending an encrypted `decision.conflict.resolved` event that points to one answer from the current question revision.
- Keep every answer event and pointer. Resolution changes the materialized state to `answered`; it never deletes or rewrites an answer.
- Supersede a question by appending an encrypted `decision.question.superseded` event, incrementing `question_revision`, and resetting the materialized state to `open`.
- Scope answer equality, conflict counting, listing, and resolution to `(decision_id, question_revision)`.
- Use schema version 7 to replace the answer uniqueness rule with `(decision_id, question_revision, answer_hash)`, allowing the same semantic answer to be given again after a legitimate question revision.
- Bind both transitions to the current repository commit and expected question revision. Stale commands append no event.
- Expose use-case-specific Wails methods. The renderer may choose an existing answer ID but cannot supply the authoritative repository revision.

## Consequences

Current review can explain all conflicting answers and the selected winner while the encrypted ledger retains the complete history. Superseded questions do not contaminate the current answer set. The forward-only schema migration rebuilds only the answer-pointer table; encrypted answer events remain unchanged.

## Verification

- conflict resolution selects one answer while preserving both answer rows;
- idempotent resolution replay appends no event;
- supersession increments the revision and exposes no old answers;
- the same semantic answer is accepted once per question revision;
- stale old-revision answers are rejected;
- desktop parsing and controls handle bounded answer lists.
