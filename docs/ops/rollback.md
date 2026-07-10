# Rollback

Code rollback never assumes data rollback. Database migrations are forward-oriented; risky upgrades create an encrypted backup and preflight before mutation. If the previous binary cannot read the new schema, recovery restores the compatible backup or completes a forward repair with explicit user evidence.

Worker/projection/index failures prefer restart, reconciliation, and rebuild. Key loss, corrupt encrypted payloads, leaked projection content, revoked devices, and bad updates use distinct runbooks. Rollback cannot resurrect logically deleted content into context or projections.

The Windows installer never deletes the Vault during uninstall or downgrade. A signed older installer is not automatically a safe rollback artifact: the previous binary must still support the current schema, or recovery must use a compatible encrypted backup. The disposable upgrade smoke test proves data retention only; it does not prove schema rollback compatibility.
