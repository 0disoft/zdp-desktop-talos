# Auto Update

- Status: Deferred until Beta

Alpha uses manual signed downloads. Automatic update is enabled only after the client verifies a signed manifest and signed artifact, creates an encrypted pre-migration backup, performs a migration preflight, retains a rollback artifact, and passes N-1 upgrade recovery tests.

Checksum-only verification is insufficient because it detects corruption but not publisher identity. The update transport must fail closed on missing, invalid, expired, or wrong-channel signatures. An update cannot silently weaken Vault encryption, data retention, model egress policy, or OS support.
