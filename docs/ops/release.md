# Release

Channels are `dev`, internal `nightly`, limited `alpha`, public `beta`, and `stable`. Promotion requires current-platform build evidence, migration and recovery fixtures, security/license gates, signed artifacts, release notes, and an owned rollback path.

Application, external protocol, database schema, event payload, worker IPC, and projection formats have independent versions. Alpha begins with manual signed distribution. Automatic update is a Beta capability after its separate gate passes.

For Windows amd64, the source-owned release path is `packaging/windows/package.ps1`. A provisioned signing host must supply NSIS, SignTool, and a code-signing certificate in the current-user certificate store. The generated receipt is checked with `verify-package.ps1 -RequireSignature`. Release promotion also runs `upgrade-smoke.ps1` against the signed N-1 and candidate installers in disposable Windows CI. Local source-contract tests do not substitute for those native artifact checks.

The signing and upgrade workflows are manual and accept runs only from `main`. Signing runs on a dedicated key-bearing runner; upgrade installation runs on a distinct throwaway runner without the signing key. An uploaded artifact is still not a release until its receipt and upgrade evidence are attached to the release decision.
