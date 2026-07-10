# ADR 0002: Contract Sources of Truth

- Status: Accepted
- Date: 2026-07-10

## Decision

- `docs/product/02-spec.md` owns product scope and user-visible guarantees.
- `docs/adr/*.md` owns durable architecture decisions.
- versioned schemas under the future `contracts/` directory own transport and export object shapes.
- the encrypted local event ledger owns historical runtime facts.
- materialized local tables own efficient current-state queries and are updated transactionally with events.
- projections, indexes, model summaries, and sync packs are derived and rebuildable.
- ZDP platform architecture owns shared signup and account contracts; Talos stores only the references and local consent/egress receipts needed for its behavior.

## Consequences

Markdown edits do not silently mutate runtime memory. Imported projections cannot become stable memory without validation and the Memory Gate. Git is never used as a real-time database. Contract changes require a schema version, fixtures, compatibility evidence, and a matching ADR when the ownership boundary changes.
