# Desktop Security

- Status: Accepted baseline
- Owner: Talos security boundary

## Trust Model

The model is an untrusted planner. Repository files, Git history, terminal output, imported memory, MCP responses, and rendered Markdown are untrusted data. Policy decisions are deterministic and auditable.

## Required Controls

- renderer exposes use-case services, never generic file, SQL, credential, or process APIs;
- worker receives task-scoped paths and single-purpose capabilities, not Vault keys or broad environment inheritance;
- processes use executable plus argv, bounded output, timeout, process-tree cancellation, canonical paths, and environment allowlists;
- network egress is default-deny and bound to structured destinations;
- remote HTML is not loaded in the privileged WebView; Markdown is sanitized and external links open outside the app;
- update manifests and artifacts require publisher signatures;
- redaction and secret scanning run before persistence, diagnostics, model egress, projection, and sync export;
- sync export uses an explicit event-type/schema allowlist; imported, device-local, unsupported, and legacy events cannot silently cross or mutate another device boundary;
- permission grants are `allow_once`, `allow_task`, `allow_workspace`, `deny`, or `require_review`; blanket shell permission is forbidden.

## Honest Limitation

A separate worker and policy broker do not prevent same-user malware or hostile test code from reading every OS-accessible file. Talos must not claim a security sandbox until platform-specific containment is implemented and tested.
