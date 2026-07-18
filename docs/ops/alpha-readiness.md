# Alpha readiness

- Status: Local engineering gates pass; distribution gates blocked
- Target: Windows amd64 private alpha

## Current evidence

| Gate | State | Current evidence |
| --- | --- | --- |
| Local Vault confidentiality | pass | current-user DPAPI, encrypted events and artifacts, plaintext-marker tests, restart-safe hard purge |
| Workspace and task boundary | pass | canonical read-only Git inspection, immutable baselines, revisioned contracts |
| Execution safety | pass with declared limitation | capability policy, owned worktrees, argv execution, bounded worker IPC, cancellation, evidence freshness; no OS sandbox claim |
| Decision and patch review | pass | encrypted decisions, conflict handling, fresh evidence, bounded diff review, explicit apply or discard |
| Model boundary | pass locally | explicit egress consent, redaction, strict response validation, durable receipts; funded live-provider smoke remains external |
| Memory boundary | pass locally | provenance, review gate, expiry, supersession, deterministic evaluation, public-only projection |
| Manual sync foundation | partial | signed encrypted packs, trusted device membership, DPAPI local signing identity, authority-bound cross-device revocation with unknown-target tombstones, contiguous validation/export journals, explicit path-free Task v2/Decision v2/Memory v2/revocation allowlist, schema-18 canonical replay, schema-19 enrollment journal, schema-23 terminal cancellation/expiry with restart reconciliation and recipient response erasure, expiry-bounded encrypted offer/acceptance, schema-20 device-local workspace remapping with Git baseline verification, schema-21 legacy Task snapshot bridge, schema-22 legacy Memory snapshot bridge with cross-path context assembly, atomic bounded folder exchange, explicit side-effect-free renderer controls, durable conflict/quarantine outcomes, exact-pack recovery, two-independent-Vault round trips, and export secret scanning pass; explicit Git exchange is not complete |
| Shared account boundary | blocked upstream | renderer-safe local status and offline unlink pass; live product-link route and production end-to-end are not approved |
| Source build and migration | pass | Go suite, Svelte diagnostics and build, schema 0 through 23 migration, desktop/worker/CLI build, doctor |
| Scaffold and package source contract | pass | strict ssealed validation and Windows package contract tests |
| Signed installer | blocked | requires a provisioned signing host, NSIS, SignTool, and the current-user certificate |
| Native install and upgrade | blocked | requires clean-install, signed N-1 upgrade, uninstall retention, explicit purge, and rollback evidence on disposable Windows runners |
| Hosted CI | blocked externally | account usage or billing gate prevents a current hosted run; local evidence does not replace it |

## Promotion boundary

Do not call this build production-ready. A limited private alpha can be considered only after the signed-installer and native install/upgrade rows pass on their owned runners. Product-link creation, unattended update, automatic Git push, OS sandbox claims, relay sync, and automatic stable-memory promotion stay disabled regardless of source-test results.

## Rollback

Alpha distribution remains manual. Keep the previous signed installer and its receipt, never migrate a Vault without the forward-only migration backup policy, and disable the affected feature rather than weakening signature, Vault, permission, evidence, export, or account-readiness gates.
