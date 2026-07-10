# Architecture Decision Records

- Status: Active
- Owner: ZDP/Talos engineering

ADRs record decisions that are expensive to reverse or that establish ownership, trust, storage, protocol, release, or platform boundaries. Product requirements remain in `../product/02-spec.md`; an ADR explains why the architecture satisfies them.

## Index

- `0001-initial-architecture-boundaries.md`: private modular monolith, desktop/worker/CLI split, ZDP identity boundary
- `0002-contract-source-of-truth.md`: ledger, materialized state, schema, projection, and platform-contract authority
- `0003-phase-0-runtime-and-encryption.md`: pinned Phase 0 runtime, IPC, encryption, and production-readiness boundary
- `0004-windows-dpapi-key-store.md`: current-user DPAPI persistence, reference binding, rotation, and unsupported-platform behavior
- `0005-windows-packaging-and-signing.md`: per-user NSIS packaging, current-user certificate signing, receipts, and Vault retention

Use `0000-template.md` for new decisions. Accepted ADRs are superseded by a new ADR instead of silently rewritten when the decision itself changes. Clarifications that do not alter the decision may be edited with an explicit rationale and validation evidence.
