# ADR 0049: Path-free Task sync v2

- Status: Accepted
- Date: 2026-07-18

## Context

ADR 0048 made repository execution depend on a stable workspace identity and a verified device-local mapping, but Task v1 events still embedded the creating device's absolute path in their encrypted payload. Encryption prevented casual disclosure while leaving the data model wrong: event equality, workspace scope, and future compatibility still depended on one machine's filesystem layout.

Simply redacting the payload during export would change canonical event bytes under the same event ID. Dropping all v1 events would strand existing local Task history. The sync contract needs a new schema and an explicit bridge rather than an in-place rewrite.

## Decision

- Task Contract schema v2 carries `workspace_id` and `source_workspace_hash` and forbids `workspace_root`. New create and revise events use v2.
- Export accepts only Task v2 create, revise, and snapshot events. Task v1 remains readable locally but is not exportable.
- Add schema 21 `task_sync_snapshots`. On startup, each local Task v1 contract revision receives exactly one new `task.contract.snapshot` v2 event. The bridge stores the source and snapshot event IDs without replacing the original Task or contract pointer.
- Do not create snapshots from imported v1 events. This prevents a receiving device from laundering a remote legacy event into a new locally originated export.
- Snapshot revision 1 materializes a missing Task. Later snapshots use the same optimistic revision and immutable workspace/baseline checks as a normal revision. Missing predecessors quarantine rather than skipping history.
- Replay validates the Vault-bound workspace identifier against the source hash. A remote absolute path, mismatched hash, unsupported schema, revision gap, or changed baseline cannot mutate Task state.
- Local reads always resolve the stable identifier through the active device mapping. Sync replay never requires a local path and therefore can preserve remote state before the user selects a repository.

## Consequences

New sync packs contain no Task workspace path, and old local contract revisions remain transferable without rewriting history. Target devices can replay Task state while continuing to fail closed for repository work until mapping succeeds. The source and target contract event IDs differ for legacy bridged revisions by design; the signed snapshot event is the shared sync provenance.

Memory v1 workspace scope remains path-derived and must move to `workspace_id` before Phase 8 can claim path-independent context assembly.

## Verification

- new Task events use schema v2 and contain no local root bytes;
- v1 events are excluded from export while one path-free snapshot per local legacy revision is selected;
- reopening the same database does not create duplicate bridge events;
- imported Task v2 replay remains unmapped until local baseline verification, then resolves to a different device path;
- malformed workspace identifiers, source hashes, and revision order receive durable quarantine or conflict outcomes.
