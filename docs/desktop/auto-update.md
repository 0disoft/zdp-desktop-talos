# Auto Update

- Status: Deferred until Beta

Alpha uses manual signed downloads. Automatic update is enabled only after the client verifies a signed manifest and signed artifact, creates an encrypted pre-migration backup, performs a migration preflight, retains a rollback artifact, and passes N-1 upgrade recovery tests.

The local update-preparation core now verifies an exact-byte Ed25519-signed `talos.update-manifest/1`, checks channel, target, version, expiry, installer size and SHA-256, then creates and preflights an encrypted Vault backup. It persists a path-free current-user-protected preparation that binds the release, signing key, installer, Vault revision, and backup identity. Authorization repeats installer verification and backup preflight and fails closed when any evidence is missing, expired, corrupt, or stale.

This core is deliberately not assembled into the renderer or an installer launcher. No production manifest public key has been provisioned, and accepting a key from the payload, environment, CLI, or renderer would make publisher verification meaningless. Network download, background polling, installer execution, restart handoff, and automatic rollback remain disabled Beta gates.

The Phase 0 Windows work adds a signed manual installer, strict package receipt, and a signed package-only release probe for real-Vault N-1 evidence. The preparation core adds a strict local manifest parser and pre-install evidence gate, but not an update endpoint, background downloader, automatic installer launch, or rollback mutation. Passing local contracts or even one native version pair therefore does not advance this document's status by itself.

Checksum-only verification is insufficient because it detects corruption but not publisher identity. The update transport must fail closed on missing, invalid, expired, or wrong-channel signatures. An update cannot silently weaken Vault encryption, data retention, model egress policy, or OS support.
