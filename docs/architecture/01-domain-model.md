# Domain Model

- Status: Accepted baseline

| Aggregate | Responsibility |
|---|---|
| Vault | encryption boundary, retention, device membership |
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
| MemoryCandidate / MemoryRecord | gated durable knowledge with provenance |
| Projection | disposable human-readable derivative |
| SyncBatch | immutable encrypted event pack |
| SecretFinding / RedactionRecord | secret detection and removal evidence |

## Key Invariants

- one repository has at most one active Run;
- one Task Contract revision never changes its baseline;
- new Task Contract confirmations carry at least one structured verification command; persisted legacy revisions without commands remain readable but cannot be reused as a new confirmation without adding one;
- verification commands contain only a policy rule ID, exact argument array, and repository-relative working directory; executable resolution, environment, timeout, and output limits remain runtime policy;
- Task Contract revision updates require the current expected revision; concurrent incompatible updates never use last-write-wins;
- Task creation and its first encrypted contract revision commit atomically; materialized contract pointers never duplicate private contract bodies;
- repository snapshots use a canonical worktree root, an exact commit baseline, bounded porcelain-v2 changes, and a capture time;
- stale Decision revisions cannot resolve the current question;
- Decision questions and answers remain encrypted; equivalent answers converge and incompatible answers move the Decision to `conflicted` without overwriting evidence;
- duplicate commands and events produce at most one side effect;
- evidence becomes stale when the repository revision or diff hash changes;
- approved memory requires evidence and an applicability scope;
- a secret value cannot enter event payloads;
- imported data must pass schema, signature, sequence, sensitivity, and memory gates.
