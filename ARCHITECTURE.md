# Architecture

- Status: Accepted baseline

## Shape

Talos starts as a modular monolith with three executable surfaces: the Wails desktop process, a separate worker process, and `talosctl`. Domain and application packages must not import Wails, SQLite drivers, model SDKs, or Git implementations.

```text
Svelte renderer
    -> use-case Wails bindings
Go control plane
    -> task runtime / decisions / memory / verification
    -> ports
        -> SQLite, model, Git, process, key store adapters
    -> scoped IPC
Restricted worker
    -> task worktree files, process execution, Git reads/writes
```

## Authority

- The encrypted local Vault is runtime truth.
- The event ledger records meaningful events; materialized tables serve current state.
- Projections and sync packs are rebuildable derivatives.
- The model proposes plans and tool intents. Deterministic code grants capabilities and executes tools.
- The model runtime records metadata-only encrypted egress receipts and accepts only Task Contract verification-command references; provider wire types never enter the core.
- The Memory Gate stores full candidate and transition snapshots only in encrypted events. Materialized memory rows contain lifecycle and scope metadata, never the remembered statement or rationale.
- Context Assembly selects only approved or stable memories under workspace, applicability, item, and byte limits and returns provenance plus a local selection reason with the plan.
- ZDP identity establishes the account link but does not become the owner of local task or memory state.

## Non-negotiable Boundaries

- The renderer gets use-case APIs, never generic file, SQL, or shell access.
- The worker never receives the Vault root key or provider credential plaintext.
- Repository text and model output are untrusted input.
- Approved memory is also untrusted model context: it may guide planning but cannot modify a Task Contract, capability, approval, or completion rule.
- Secret values are rejected before event persistence, model egress, diagnostics, and projection export.
- Completion requires evidence bound to the current repository revision and diff hash.
- The primary worktree is not modified during agent execution.

Detailed contracts live under `docs/architecture/` and decisions under `docs/adr/`.
