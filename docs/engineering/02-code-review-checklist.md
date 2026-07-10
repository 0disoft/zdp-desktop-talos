# Code Review Checklist

- source-of-truth and ownership boundary are named;
- domain invariants are enforced below the UI;
- external data is parsed and bounded before it becomes trusted state;
- path, symlink, argv, environment, output, timeout, and cancellation behavior is explicit;
- permission and egress scopes did not broaden accidentally;
- idempotency, stale revisions, concurrency, and crash recovery are covered;
- sensitive data does not enter logs, errors, fixtures, model payloads, or projections;
- verification evidence becomes stale after relevant changes;
- migrations have preflight, backup, compatibility, and failure recovery;
- tests include denied, malformed, duplicate, interrupted, and stale cases;
- user-visible claims do not overstate sandboxing, deletion, verification, or sync.
