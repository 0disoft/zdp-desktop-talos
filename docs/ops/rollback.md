# Rollback

Code rollback never assumes data rollback. Database migrations are forward-oriented; risky upgrades create an encrypted backup and preflight before mutation. If the previous binary cannot read the new schema, recovery restores the compatible backup or completes a forward repair with explicit user evidence.

Worker/projection/index failures prefer restart, reconciliation, and rebuild. Key loss, corrupt encrypted payloads, leaked projection content, revoked devices, and bad updates use distinct runbooks. Rollback cannot resurrect logically deleted content into context or projections.
