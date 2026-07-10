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
- stale Decision revisions cannot resolve the current question;
- duplicate commands and events produce at most one side effect;
- evidence becomes stale when the repository revision or diff hash changes;
- approved memory requires evidence and an applicability scope;
- a secret value cannot enter event payloads;
- imported data must pass schema, signature, sequence, sensitivity, and memory gates.
