# ADR 0022: Deterministic Process Permission Broker

- Status: Accepted
- Date: 2026-07-11

## Context

The worker can enforce a capability, but it must not decide whether a task is allowed to receive that capability. Repository content, model output, process arguments, and worker frames are untrusted. A persisted user grant must remain bound to the task or workspace and to the exact execution intent that was reviewed.

## Decision

The main process owns a deterministic Permission Broker between the task contract and worker IPC.

- A product process rule fixes the executable, required argument prefix, maximum argument count, environment-name allowlist, timeout, output limit, and default outcome.
- Shell executables are not valid process rules.
- The task record, immutable contract revision, workspace root, baseline commit, and process intent must agree before a rule or grant is considered.
- `process.exec` and `process.exec:<rule-id>` task-contract prohibitions override every product rule and user grant.
- User grants bind a canonical SHA-256 intent hash. Task grants include the task identity; workspace grants omit only the task identity and still bind the workspace and exact execution shape.
- Environment names are a canonical, duplicate-free set for hashing. Windows environment names are compared case-insensitively.
- Active explicit deny grants win. Remaining matching grants are ordered by scope (`allow_once`, `allow_task`, `allow_workspace`) and then grant ID so input ordering cannot change the result.
- An allowed evaluation emits a capability whose argument prefix is the complete reviewed argument vector and whose maximum argument count equals that vector length. The worker therefore cannot append unreviewed arguments.
- `allow_once` is marked for consumption. Durable atomic consumption with attempt creation belongs to the execution-journal transaction and is not claimed by this ADR.

Stable reason codes describe denials and review requirements without returning internal errors to the renderer.

## Consequences

The model can propose a process intent but cannot authorize it. The worker receives only the narrow capability produced by deterministic code. The same validated input and grant set produces the same result.

This remains policy isolation rather than an OS security sandbox. A same-user process may still access resources the operating system permits unless a later platform sandbox contains it.

SQLite schema v8 stores grants, runs, and attempts, atomically consumes `allow_once` with attempt creation, rejects deny grants at the storage boundary, and reconciles interrupted pending attempts to `unknown` after restart. User-review UI and main-process dispatch assembly still remain; callers must not claim end-to-end execution authorization until those surfaces use the journal transaction.
