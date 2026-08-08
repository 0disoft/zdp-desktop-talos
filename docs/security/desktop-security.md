# Desktop Security

- Status: Accepted baseline
- Owner: Talos security boundary

## Trust Model

The model is an untrusted planner. Repository files, Git history, terminal output, imported memory, MCP responses, and rendered Markdown are untrusted data. Policy decisions are deterministic and auditable.

## Required Controls

- renderer exposes use-case services, never generic file, SQL, credential, or process APIs;
- worker receives task-scoped paths and single-purpose capabilities, not Vault keys or broad environment inheritance;
- processes use executable plus argv, bounded output, timeout, process-tree cancellation, canonical paths, and environment allowlists;
- worker IPC never returns raw stdout or stderr to the desktop process; it returns bounded byte counts and SHA-256 digests, closes a broken response transport, and gives each tool response an independent deadline;
- external worker and model calls use cancelable Vault session leases rather than holding the Vault mutex; lock and purge cancel and drain those leases before closing session storage;
- the built-in Go verification rule uses Talos-owned HOME, GOPATH, build-cache, and module-cache directories plus `GOTOOLCHAIN=local`, `GOPROXY=off`, `GOSUMDB=off`, `GOENV=off`, and `GOFLAGS=-mod=readonly`; renderer input cannot replace those values;
- process rules declare security-relevant effects. A Task Contract that forbids an effect such as `network.egress` is denied before grants are considered. Because process containment does not yet prove OS-level network isolation, Go tests remain network-capable code and require an explicit Task Contract opt-in;
- verification, patch review, apply, and discard resolve the backend-owned active Workspace immediately before loading the Task, then require canonical root and baseline equality before any permission, worktree, or repository side effect; renderer state is not the workspace authority;
- Windows tool processes start suspended, join their kill-on-close Job Object before their sole initial thread resumes, and fail closed when that activation sequence cannot be proven;
- network egress is default-deny and bound to structured destinations;
- remote HTML is not loaded in the privileged WebView; Markdown is sanitized and external links open outside the app;
- update manifests and artifacts require publisher signatures;
- redaction and secret scanning run before persistence, diagnostics, model egress, projection, and sync export;
- patch application fails closed when any changed file is binary, truncated, omitted, unsupported, or otherwise not fully represented in the secret-scanned review manifest;
- sync export uses an explicit event-type/schema allowlist; imported, device-local, unsupported, and legacy events cannot silently cross or mutate another device boundary;
- Git sync prepares only encrypted packs in a clean canonical repository, imports only tracked packs from an unchanged clean commit, and never runs commit, pull, push, merge, remote, credential, or configuration writes;
- Vault backup uses an online SQLite snapshot, exact hash-verified blobs, a bounded chunk-authenticated encrypted stream, same-directory no-clobber publication, and isolated preflight; it never embeds the Vault root key or replaces live data;
- live restore requires the exact preflight identity, current revision, and Vault-ID confirmation; authenticated staging, protected journaling, preserved originals, restart reconciliation, and post-promotion integrity checks fail closed before the Vault returns to active use;
- permission grants are `allow_once`, `allow_task`, `allow_workspace`, `deny`, or `require_review`; blanket shell permission is forbidden.

## Honest Limitation

A separate worker, policy broker, and pre-execution Windows Job Object membership do not prevent same-user malware or hostile test code from reading every OS-accessible file. Talos must not claim a security sandbox until a restricted-token or equivalent platform boundary is implemented and tested.
