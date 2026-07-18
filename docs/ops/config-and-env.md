# Configuration and Environment

Configuration is scoped to application, Vault, workspace, task, and provider. Lower scopes may narrow but cannot silently override product security policy. Child processes receive a freshly constructed allowlisted environment, never the full desktop-process environment.

Paths are canonicalized and resolved against an owned root. Configuration values affecting egress, credentials, retention, deletion, signing, update channel, or capabilities are validated and surfaced to the user. Unknown configuration is rejected or quarantined with a version error.

## Model planning

- `OPENAI_API_KEY` is a credential handle consumed only by the fixed-host OpenAI Responses adapter at request time. Talos reports only the environment-variable name and availability state.
- `TALOS_OPENAI_MODEL` is required and must match the bounded provider/model key syntax. Talos does not compile a moving default model alias into the desktop.
- Missing or invalid values disable hosted plan proposals without disabling local Vault, Task, Decision, Memory, verification, or patch-review behavior.
- Every proposal requires a fresh renderer confirmation bound to provider, model, canonical workspace root, baseline commit, and Task Contract revision. Configuration changes invalidate the confirmation.
- Local and CI contract tests use a fake HTTP transport. A live request is never part of ordinary validation and requires a separately authorized funded-account smoke.
