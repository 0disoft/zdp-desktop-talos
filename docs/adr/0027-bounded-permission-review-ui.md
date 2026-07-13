# Bounded Permission Review UI

- Status: Accepted
- Owner: ZDP/Talos engineering

## Decision

The MVP permission review surface exposes only `deny`, `allow_once`, and `allow_task`. Workspace-wide grants remain an internal storage capability and are rejected by the application service.

Grant expiry is server-owned: one-time grants expire after 15 minutes; task grants and denials expire after 24 hours. The renderer cannot submit an expiry, capability hash, executable, argument vector, task scope, or workspace scope.

Wails exposes only `PermissionService.List(taskID)` and `PermissionService.Resolve(requestID, outcome, requestToken)`. It does not expose generic grant creation, arbitrary process execution, SQL, or worker IPC.

The review screen displays the exact executable and argv plus timeout and rule identity. Environment values are never returned; only requested environment names may be displayed. Locking the Vault clears permission requests from renderer state.

## Consequences

- A compromised renderer cannot widen a request or create a workspace-wide grant through the MVP API.
- Repeated resolution tokens are idempotent and conflicting reuse fails closed.
- Task grants currently expire by time rather than task terminal state because the MVP Task lifecycle has no terminal transition yet.
