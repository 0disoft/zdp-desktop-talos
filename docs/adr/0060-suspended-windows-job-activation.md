# ADR 0060: Suspended Windows job activation

- Status: Accepted
- Date: 2026-07-20
- Owner: ZDP/Talos engineering

## Context

ADR 0021 placed each Windows tool process in a Job Object configured with
`KILL_ON_JOB_CLOSE`, but `exec.Cmd.Start` allowed the process to run before the
worker attached it to that Job Object. A hostile executable could use that
small scheduling window to create a descendant outside the process tree that
Talos later terminates.

The worker still needs Go's bounded `exec.Cmd` pipe and wait behavior. Replacing
the complete Windows process launcher would duplicate command-line quoting,
environment, handle-list, and pipe inheritance logic from the standard library.

## Decision

- Configure every Windows tool command with `CREATE_SUSPENDED` before calling
  `exec.Cmd.Start`. The primary thread cannot enter untrusted user-mode code
  while Talos installs containment.
- Create the Job Object before process creation and keep
  `KILL_ON_JOB_CLOSE` mandatory.
- After process creation, assign the suspended process to the Job Object before
  opening or resuming its initial thread.
- Enumerate the new process's threads and require exactly one initial thread.
  Missing or multiple threads are containment failures, not compatibility
  fallbacks.
- Resume that thread only when `ResumeThread` reports a previous suspension
  count of exactly one. Any other count fails closed.
- On assignment, enumeration, open-thread, or resume failure, kill and reap the
  process and close the Job Object. Never retry by starting the command without
  suspended containment.
- Keep the Unix process-group path unchanged; this ADR governs Windows only.

## Consequences

Untrusted tool code no longer receives a scheduling window before Windows Job
Object membership. Timeout, cancellation, output-budget termination, and Job
Object close continue to terminate descendants created after activation.

This is stronger process-tree containment, not an OS security sandbox. The
process still runs as the current user and can access files, registry keys,
network destinations, and other resources allowed to that user unless a later
restricted-token or platform sandbox boundary denies them.

Thread discovery uses a system thread snapshot and therefore adds bounded
platform overhead before execution. The command timeout continues to include
startup and activation time rather than hiding containment cost from the
caller.

## Verification

- A Windows regression test starts a marker-writing helper, proves the marker
  is absent while the process is suspended, activates the Job Object, and then
  observes successful execution.
- Existing executor tests cover argv and environment policy, output limits,
  timeout, cancellation, and normal execution through the same activation path.
- The descendant-sentinel test proves a timed-out Windows process cannot leave
  its child alive.
- The full Go suite covers the worker IPC and main-process execution consumers.
