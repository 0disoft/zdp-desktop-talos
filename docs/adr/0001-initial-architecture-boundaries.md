# ADR 0001: Initial Architecture Boundaries

- Status: Accepted
- Date: 2026-07-10

## Decision

Build Talos as a private modular monolith with a Wails desktop transport, Svelte renderer, Go control plane, separate restricted worker, and companion CLI. Keep domain and application code independent of Wails, SQLite drivers, model SDKs, and Git implementations. Execute repository modifications only in task-scoped Git worktrees.

ZDP shared signup supplies identity, consent, membership, and device-registration contracts. Talos retains authority over the local Vault, tasks, decisions, evidence, and memories. Identity linking does not imply content synchronization.

## Consequences

The first repository remains one deployable product instead of premature service or repository fragmentation. Wails can be replaced at the transport boundary. A separate worker improves cancellation and protocol control but is not advertised as an OS security sandbox. Relay, protocol extraction, integrations, and multi-device automation remain deferred.
