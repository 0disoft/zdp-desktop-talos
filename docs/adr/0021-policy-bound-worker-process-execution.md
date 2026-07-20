# ADR 0021: Policy-bound worker process execution

- Status: Accepted
- Date: 2026-07-11

## Context

A worker that accepts a shell string or arbitrary executable is only a remote shell with extra framing. It must remain responsive to cancellation while a tool runs, bound memory under hostile output, avoid inheriting desktop secrets, and terminate descendants rather than only the direct process.

## Decision

- Construct each executor from one canonical task-worktree root and a trusted capability manifest.
- A process capability names one absolute executable, an exact argv prefix, allowed environment variable names, maximum timeout, and combined stdout/stderr byte budget.
- Reject common shell executables even when included in a manifest. The worker never interprets command strings, pipes, redirects, substitutions, or shell metacharacters.
- Accept only relative working directories whose canonical existing target remains inside the task worktree.
- Build a fresh child environment from minimal operating-system variables plus capability-allowed request values. Never inherit the parent environment wholesale.
- Default command output to a combined 1 MiB and cap it absolutely at 4 MiB. Terminate the process tree when the combined budget is exceeded.
- Cap execution at ten minutes; a capability may only narrow that limit.
- On Windows, place the process in a Job Object configured with `KILL_ON_JOB_CLOSE`. On Unix-like systems, create and terminate a process group.
- Extend protocol version 1 through advertised capabilities with `start_run`, `execute_tool`, and `cancel_tool`. A run policy must be accepted before execution.
- Keep the worker read loop active during execution. Serialize frames, allow one active tool call, reject duplicate IDs, and cancel/reap active work before shutdown.
- Return bounded byte fields and stable terminal states without exposing raw internal errors or environment values.

## Consequences

The worker can execute and cancel a policy-approved direct process without becoming a generic shell. This remains policy isolation, not an OS sandbox. ADR 0060 later closes the Windows pre-attachment scheduling race by starting the process suspended, assigning it to the Job Object, and only then resuming its sole initial thread.

The deterministic Permission Broker now converts task- or workspace-scoped grants into exact worker capabilities and keeps contract denial authoritative. Durable grant consumption, the Run/Attempt journal, user-review UI, and main-process assembly are still required before model-generated tool intents can reach `start_run` or `execute_tool`.

## Verification

- argv prefix, capability, environment, shell, and worktree-escape denials;
- fresh environment proves an unrelated parent secret is absent;
- combined stdout/stderr output cap terminates the process;
- timeout and explicit IPC cancellation return bounded terminal states;
- Windows descendant sentinel proves a child process does not survive timeout;
- execution before `start_run`, duplicate active IDs, concurrent calls, and mismatched runs are rejected;
- shutdown cancels and reaps active work before returning.
