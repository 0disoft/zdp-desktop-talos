# ADR 0041: Deterministic public memory projection

- Status: Accepted
- Date: 2026-07-18

## Context

The ledger and materialized memory state are authoritative, but users also need disposable, human-readable files that can be inspected with ordinary tools and regenerated after deletion. A naive dump would put private statements, workspace paths, Vault identifiers, raw evidence links, rejected candidates, or secrets into Git history. Adding a timestamp would also make every rebuild appear changed when the accepted memory state did not change.

## Decision

- Compile three deterministic files from the same ordered record model: `memory/memories.md`, `memory/memories.yaml`, and `memory/memories.jsonl`.
- Include only `public` records in approved, stable, stale, deprecated, or superseded state. Exclude private and sensitive records structurally before rendering. Exclude candidate, rejected, and quarantined states even when mislabeled public.
- Never project the Vault ID, workspace root, raw evidence event IDs, or provider credentials. Preserve only the scope kind, a bounded source reference, and evidence count needed to explain origin without exporting local paths or payloads.
- Run the deterministic secret scanner over every complete rendered file. Any finding or scanner failure rejects the whole projection; Talos does not produce a partially redacted export that could be mistaken for complete output.
- Sort records by memory ID, omit generation time, hash every file, and derive the bundle hash from ordered path and content digests. The same accepted memory state therefore produces byte-identical output regardless of database iteration order.
- Escape untrusted statement text in Markdown. YAML uses quoted scalar values and JSONL uses one versioned record per line.
- Expose only a bounded read-only desktop preview. It returns file metadata, hashes, at most 64 KiB per preview, and a `complete` flag. It neither writes to an arbitrary filesystem path nor commits or pushes Git state.

## Consequences

Projection becomes a disposable view instead of a second memory database. A user can see exactly what a future export would contain without granting renderer filesystem access. The current preview deliberately stops at 200 records; immutable encrypted pack creation, full pagination, destination ownership, import validation, and Git exchange remain the other half of Phase 8.

## Verification

- compiler tests prove deterministic output across input order and structural exclusion of unsafe records;
- leak tests prove Vault IDs, workspace paths, and raw evidence IDs are absent;
- secret fixtures reject the entire result;
- Markdown fixtures prove HTML and table delimiters are escaped;
- Wails tests prove locked-Vault rejection, bounded previews, explicit completeness, and stable hashes;
- frontend type checks prove the renderer rejects oversized or malformed projection DTOs.
