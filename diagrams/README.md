# Diagrams

- Status: Architecture baseline
- Owner: ZDP/Talos engineering

These Mermaid sources summarize accepted contracts; the written product specification and ADRs remain authoritative when a diagram drifts.

- `system-context.mmd`: developer, Talos, ZDP core, model, Git, Vault, and future sync boundary
- `container-view.mmd`: renderer, control plane, domain/application, adapters, worker, CLI, and worktree
- `core-runtime-flow.mmd`: contract-to-execution-to-memory closed loop
- `release-flow.mmd`: validation, native build, signing, promotion, and release block
- `rollback-flow.mmd`: data-aware recovery rather than blind binary rollback

Update the matching written contract and diagram together when ownership, trust zones, runtime order, release gates, or recovery behavior changes.
