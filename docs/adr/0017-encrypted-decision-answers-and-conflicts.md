# ADR 0017: Encrypted Decision answers and conflicts

- Status: Accepted
- Date: 2026-07-11

## Context

Decision questions contain private task context, risk explanations, and implementation consequences. Answers may arrive repeatedly or incompatibly. Last-write-wins would erase user intent, while storing question and answer bodies in materialized rows would leak them outside the encrypted event boundary.

## Decision

- Store question, reason, unanswered risk, safe default, scopes, options, and answer values only in encrypted events.
- Keep minimal Decision and answer pointers in schema version 6: category, state, question revision, expected repository revision, timestamps, hashes, and event provenance.
- Bind every answer to both `question_revision` and `expected_repository_revision`. Reject stale values before appending an event.
- Converge semantically equal answers without a new event. Preserve different answers and move the Decision to `conflicted`; never overwrite an earlier answer.
- Bind command idempotency to the complete intent while excluding generated occurrence time.
- Record the command's resulting state in each encrypted answer event so historical idempotency replay remains exact after later conflicts.

## Consequences

The current Decision state is queryable without decrypting private text, while review decrypts the question and latest answer through provenance pointers. A conflict retains both answer events for explicit later resolution. Question supersession and conflict resolution are later state transitions; schema 6 does not fake them through mutable question bodies.

## Verification

- encrypted question and answer restart round trip with plaintext-marker checks;
- stale question and repository revisions append no event or answer;
- semantically equal answers converge without a new event;
- concurrent different answers yield one `answered` result and one `conflicted` result;
- current reads remain `conflicted` while historical idempotency replay returns its original result.
