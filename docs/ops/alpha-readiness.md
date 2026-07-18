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
| Manual sync foundation | pass locally | signed encrypted packs, trusted device membership, DPAPI local signing identity, authority-bound cross-device revocation with unknown-target tombstones, contiguous validation/export journals, explicit path-free Task v2/Decision v2/Memory v2/revocation allowlist, schema-18 canonical replay, schema-19 enrollment journal, schema-23 terminal cancellation/expiry with restart reconciliation and recipient response erasure, expiry-bounded encrypted offer/acceptance, schema-20 device-local workspace remapping with Git baseline verification, schema-21 legacy Task snapshot bridge, schema-22 legacy Memory snapshot bridge with cross-path context assembly, atomic bounded folder exchange, clean tracked-only Git exchange with manual commit/pull/push, explicit side-effect-free renderer controls, durable conflict/quarantine outcomes, exact-pack recovery, two-independent-Vault round trips, and export secret scanning pass |
| Encrypted backup and recovery | pass locally | SQLite online snapshot, exact ready-blob capture, chunk-authenticated encrypted archive, no-clobber publication, full event/blob integrity verification, isolated current-binary migration rehearsal, confirmation-bound journaled live restore, four generation-swap crash-point resumptions, and corrupt-promotion rollback; lost-key recovery is not implemented |
| Shared account boundary | blocked upstream | renderer-safe local status and offline unlink pass; live product-link route and production end-to-end are not approved |
| Source build and migration | pass | Go suite, Svelte diagnostics and build, schema 0 through 23 migration, desktop/worker/CLI build, doctor including encrypted online-backup, isolated-preflight, and journaled-live-restore self-tests |
| Scaffold and package source contract | pass | strict ssealed validation and Windows package contract tests |
| Update preparation | core passes locally; activation blocked | strict signed manifest and artifact verification, protected path-free backup binding, repeat authorization, signed package-only release probe, trusted signing-run ancestry, strict receipt parsing, and path-free N-1 evidence contracts pass; production publisher key, installer execution adapter, and executed native N-1 evidence are absent |
| Signed installer | blocked | requires a provisioned signing host, NSIS, SignTool, and the current-user certificate |
| Native install and upgrade | blocked | the real-Vault harness now requires clean signed N-1 install, installed-byte verification, candidate mutation, uninstall retention, exact purge, and direct old-binary read; two probe-bearing signed packages have not yet run on the disposable Windows runner |
| Hosted CI | blocked externally | account usage or billing gate prevents a current hosted run; local evidence does not replace it |

## Promotion boundary

Do not call this build production-ready. A limited private alpha can be considered only after the signed-installer and native install/upgrade rows pass on their owned runners. Product-link creation, unattended update, automatic Git push, OS sandbox claims, relay sync, and automatic stable-memory promotion stay disabled regardless of source-test results. A protected update preparation is not installer permission.

## Rollback

Alpha distribution remains manual. Create and successfully preflight an encrypted backup before a risky migration, keep the previous signed installer and its receipt, and disable the affected feature rather than weakening signature, Vault, permission, evidence, export, or account-readiness gates. Journaled restore can recover that backup with the current binary; it is not proof that the previous binary can read a newer schema.
