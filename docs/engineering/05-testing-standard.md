# Testing Standard

Domain tests cover states and invariants. Property and fuzz tests cover event ordering, duplicate import, conflict merge, path normalization, framing, and redaction. Integration tests cover SQLite/WAL recovery, encrypted blobs, Git worktrees, process cancellation, output caps, and provider schema failures. Desktop E2E covers the real main/worker boundary.

The malicious-repository corpus includes prompt injection, symlinks outside the repository, hostile hooks/helpers, home-directory reads, endless child processes, huge output, ANSI/HTML payloads, case collisions, Unicode normalization, dirty state, and detached HEAD.

PR tests use fake providers and recorded fixtures. Live provider contracts run separately with dedicated credentials and bounded cost. A test passed before the final patch mutation is stale evidence.
