# Installers

- Status: Phase 0 spike required

The first installer format is selected with the first supported platform. Every installer must carry publisher signing, bundle the matching worker, verify executable/version compatibility on startup, preserve the Vault across application upgrades, and avoid installing provider credentials or enabling telemetry.

Direct distribution is preferred initially because arbitrary repository and development-tool execution conflicts with some app-store sandbox models. Installer creation is not release evidence by itself; clean install, upgrade, uninstall-with-data-retention, explicit data removal, and rollback scenarios must pass.
