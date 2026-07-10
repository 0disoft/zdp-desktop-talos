# Continuous Integration

CI runs formatting, static analysis, domain and integration tests, generated-code drift, contract fixtures, migration/recovery fixtures, dependency license and vulnerability checks, secret scanning, malicious-repository scenarios, and native platform builds as those surfaces appear.

PR CI does not call paid live models. Release CI additionally verifies signing, installer behavior, SBOM/notices, update metadata, N-1 migration, and artifact provenance. A skipped platform or security suite is visible and cannot be relabeled as passed.
