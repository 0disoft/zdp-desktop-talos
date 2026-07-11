# ADR 0013: Read-Only System Git Workspace Inspection

- Status: Accepted
- Date: 2026-07-11

## Context

Task Contracts need a trustworthy repository root and baseline commit before planning or execution. A renderer-supplied path is untrusted, and even read-looking Git commands can consult global configuration, invoke a filesystem monitor, prompt for credentials, refresh the index, emit unbounded output, or expose private paths and stderr through a UI error.

## Decision

- Require an already installed system Git executable. Resolve it once and keep Git behind the repository-inspection port.
- Expose only `WorkspaceService.InspectRepository(path)`, `Status`, and `Close` to Wails. Do not expose generic process, filesystem, Git-argument, or environment APIs.
- Resolve the user-selected existing directory to an absolute canonical path. Ask Git for the top-level worktree, canonicalize that result again, and require the selected path to remain inside it.
- Reject bare repositories and repositories without a baseline commit. Support clean, dirty, renamed, unmerged, untracked, branch-attached, and detached-HEAD worktrees.
- Run exact argv without a shell, with a ten-second deadline, closed stdin, bounded stdout/stderr, credential prompting disabled, optional locks disabled, and system/global Git configuration disabled. Disable fsmonitor, untracked cache, color, and quoted-path output for the inspection commands.
- Parse porcelain-v2 NUL-delimited status deterministically. Reject malformed records, escaping paths, and more than 4,096 changes.
- Keep the full change set inside the trusted application snapshot. Return only root, baseline, branch/detached state, dirty flag, change count, and capture time to the renderer.
- Map Git and path failures to stable safe UI codes without returning command stderr, environment values, or private internal causes.

## Consequences

Workspace inspection cannot execute hooks, aliases, shell fragments, credential prompts, or user-configured fsmonitor processes through this path. Disabling global configuration can reject repositories that depend on user-level `safe.directory` exceptions; that is a deliberate fail-closed boundary rather than an invitation to silently trust a different owner.

The snapshot proves repository state only at its capture instant. Task Contract confirmation must inspect again or compare the baseline and dirty-state fingerprint before accepting the snapshot as current.

## Evidence

- clean, modified, untracked, nested-directory, and detached-HEAD integration tests using system Git;
- unborn and bare repository rejection;
- porcelain-v2 tracked, rename, unmerged, untracked, traversal, and change-limit tests;
- domain snapshot state/path invariant tests;
- Wails safe error and open/close tests;
- Svelte response validation and bounded status rendering.
