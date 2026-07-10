# Continuous Integration

CI runs formatting, static analysis, domain and integration tests, generated-code drift, contract fixtures, migration/recovery fixtures, dependency license and vulnerability checks, secret scanning, malicious-repository scenarios, and native platform builds as those surfaces appear.

PR CI does not call paid live models. Release CI additionally verifies signing, installer behavior, SBOM/notices, update metadata, N-1 migration, and artifact provenance. A skipped platform or security suite is visible and cannot be relabeled as passed.

Windows PR and `main` CI run on a GitHub-hosted Windows image without signing credentials. Manual Windows release packaging runs only on the dedicated `talos-signing` self-hosted runner from `main`; signed N-1 upgrade testing runs on a separate disposable `talos-upgrade-smoke` runner. See `windows-signing-host.md` for enrollment, plan-dependent environment protections, and the remaining native evidence gates.
