# Command Contract

- Status: Phase 0 baseline

## Initial Command

```text
talosctl doctor [--json]
```

`doctor` inspects runtime, OS, architecture, Git availability, key-store support, worker handshake compatibility, and a temporary SQLite open/write/checkpoint cycle. On Windows it performs a temporary current-user DPAPI put, reopen, rotate, and delete cycle. It prints versions and availability only; it never prints environment variable values, credentials, Vault payloads, protected key blobs, or repository content. `production_blockers` lists release gates that the local probe cannot prove.

## General Rules

- stdout contains the requested human or JSON result; diagnostics go to stderr;
- `--json` returns one versioned object and never ANSI decoration;
- mutating commands require an idempotency key and expected revision when concurrency matters;
- executable and argv are represented separately; no generic shell-string command is exposed;
- a locked Vault, unsupported key store, stale revision, permission denial, and verification failure have distinct stable error codes;
- cancellation and timeout do not imply rollback unless the command contract explicitly proves it.

The full command set remains intentionally uncommitted until application use cases exist.
