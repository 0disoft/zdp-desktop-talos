# Performance Budget

The initial budgets are owned by `docs/architecture/03-quality-attributes.md`. Measurements must record OS, architecture, storage, CPU, event count, artifact count, repository size, warm/cold state, and percentile. A faster path cannot skip encryption, redaction, permission checks, evidence freshness, durability, or cancellation.

Performance regressions are release-blocking when they cause full-ledger startup replay, unbounded terminal/model buffering, UI loading of full timelines, projection rebuild during interactive work, or worker processes that outlive cancellation.
