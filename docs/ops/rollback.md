# Rollback

Code rollback never assumes data rollback. Database migrations are forward-oriented; risky upgrades create an encrypted backup and preflight before mutation. If the previous binary cannot read the new schema, recovery restores the compatible backup or completes a forward repair with explicit user evidence.

Preflight opens and migrates only an isolated copy. The separate live-restore command now uses a protected crash-safe journal, exact target identity checks, original-generation preservation, and explicit confirmation. That proves current-binary recovery from a selected backup; it still does not prove that an older binary can read the current schema or authorize an updater to run unattended.

Live restore deletes any protected update preparation after staging succeeds and before the restore journal changes the active generation. This prevents an earlier release/backup binding from surviving a Vault timeline replacement. Failure to delete that record aborts restore before live files move.

Worker/projection/index failures prefer restart, reconciliation, and rebuild. Key loss, corrupt encrypted payloads, leaked projection content, revoked devices, and bad updates use distinct runbooks. Rollback cannot resurrect logically deleted content into context or projections.

The Windows installer never deletes the Vault during uninstall or downgrade. A signed older installer is not automatically a safe rollback artifact: the previous binary must still support the current schema, or recovery must use a compatible encrypted backup. The disposable upgrade smoke test proves data retention only; it does not prove schema rollback compatibility.
