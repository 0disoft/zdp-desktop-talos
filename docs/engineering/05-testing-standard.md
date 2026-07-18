# Testing Standard

Domain tests cover states and invariants. Property and fuzz tests cover event ordering, duplicate import, conflict merge, path normalization, framing, and redaction. Integration tests cover SQLite/WAL recovery, encrypted blobs, Git worktrees, process cancellation, output caps, and provider schema failures. Desktop E2E covers the real main/worker boundary.

The malicious-repository corpus includes prompt injection, symlinks outside the repository, hostile hooks/helpers, home-directory reads, endless child processes, huge output, ANSI/HTML payloads, case collisions, Unicode normalization, dirty state, and detached HEAD.

PR tests use fake providers and recorded fixtures. Live provider contracts run separately with dedicated credentials and bounded cost. A test passed before the final patch mutation is stale evidence.

The Phase 5 fixture-provider scenario is a control-plane integration test, not model-quality evidence. It proves untrusted-context framing, secret redaction, egress receipt lifecycle, plan-schema and Task Contract validation, permission review stopping, and provider-to-executor handoff. Hosted-provider compatibility, latency, pricing, retention, and planning quality remain separate live-contract and evaluation gates.

The Phase 7 memory-loop scenario uses an encrypted SQLite Vault and deterministic fixture provider. It proves same-Vault provenance, candidate-only extraction, explicit approval, bounded applicability selection, explainable source references, untrusted framing, and a later plan changed by the approved statement. The Wails slice additionally proves that validated answered Decisions compile idempotently, malformed or cross-Vault provenance fails closed, review outcomes use optimistic revisions, and the current-Task surface returns local applicability reasons. It does not prove broader extraction precision beyond answered Decisions, stable-promotion quality, vector retrieval, or hosted-model behavior.
