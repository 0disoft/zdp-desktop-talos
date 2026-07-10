# Release

Channels are `dev`, internal `nightly`, limited `alpha`, public `beta`, and `stable`. Promotion requires current-platform build evidence, migration and recovery fixtures, security/license gates, signed artifacts, release notes, and an owned rollback path.

Application, external protocol, database schema, event payload, worker IPC, and projection formats have independent versions. Alpha begins with manual signed distribution. Automatic update is a Beta capability after its separate gate passes.
