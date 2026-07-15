# Domain Model

- Status: Accepted baseline

| Aggregate | Responsibility |
|---|---|
| Vault | encryption boundary, retention, device membership |
| AccountLink | device-local Vault membership and encrypted references to ZDP-owned account, workspace, and consent facts |
| Device | signing identity, sync sequence, revocation |
| Workspace | repository location and policy |
| RepositorySnapshot | baseline commit, dirty state, toolchain facts |
| Task | user goal and lifecycle |
| TaskContractRevision | immutable scope, capabilities, checks, completion rules |
| Run / Step / Attempt | execution, scoped blocking, retries, recovery |
| Decision / DecisionAnswer | revision-bound human choice and conflicts |
| ToolIntent / ToolCall / ToolResult | proposed, authorized, and observed action |
| PermissionGrant | capability scope and expiry |
| Artifact | encrypted diff, log, snapshot, or report |
| VerificationEvidence | proof bound to current revision and diff |
| PatchAction / TaskOutcome | idempotent external mutation journal and terminal Task projection |
| MemoryCandidate / MemoryRecord | gated durable knowledge with provenance |
| Projection | disposable human-readable derivative |
| SyncBatch | immutable encrypted event pack |
| SecretFinding / RedactionRecord | secret detection and removal evidence |

## Key Invariants

- one repository has at most one active Run;
- one Vault has at most one current device-local account membership; link, unlink, and relink require the expected revision;
- ZDP credentials, sessions, contact methods, profile fields, platform membership truth, and consent truth never enter the AccountLink aggregate;
- linked account references exist only in encrypted private events; the materialized membership row contains no raw or hashed external identity references;
- account-provider failure cannot change local Vault authority or local task availability;
- one Task Contract revision never changes its baseline;
- new Task Contract confirmations carry at least one structured verification command; persisted legacy revisions without commands remain readable but cannot be reused as a new confirmation without adding one;
- verification commands contain only a policy rule ID, exact argument array, and repository-relative working directory; executable resolution, environment, timeout, and output limits remain runtime policy;
- Task Contract revision updates require the current expected revision; concurrent incompatible updates never use last-write-wins;
- Task creation and its first encrypted contract revision commit atomically; materialized contract pointers never duplicate private contract bodies;
- repository snapshots use a canonical worktree root, an exact commit baseline, bounded porcelain-v2 changes, and a capture time;
- stale Decision revisions cannot resolve the current question;
- Decision questions and answers remain encrypted; equivalent answers converge and incompatible answers move the Decision to `conflicted` without overwriting evidence;
- duplicate commands and events produce at most one side effect;
- successful verification evidence is created atomically with the Attempt transition and is bound to the contract revision, command index, baseline commit, capability, and deterministic task-worktree state hash;
- evidence becomes stale when a later task-worktree state hash differs;
- apply requires fresh evidence, zero secret findings, contract-contained changed paths, no unresolved blocking Decision, and a clean primary worktree at the immutable baseline;
- patch action preparation precedes external Git mutation; `pending` or `unknown` actions are never automatically repeated;
- successful patch action and terminal Task outcome commit atomically; failed or unknown actions leave the Task contracted;
- approved memory requires evidence and an applicability scope;
- a secret value cannot enter event payloads;
- imported data must pass schema, signature, sequence, sensitivity, and memory gates.
