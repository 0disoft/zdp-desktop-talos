# ADR 0005: Windows Packaging and Signing

- Status: Accepted
- Date: 2026-07-10
- Owners: ZDP/Talos engineering

## Context

Phase 0 can build Windows binaries, but loose executables are not a supportable distribution. The release path must bind the desktop, worker, and CLI to one version, preserve the current-user DPAPI and Vault boundary during upgrades, and fail closed when publisher identity cannot be proven. Packaging must also avoid hiding network downloads or machine-wide mutations inside an installer.

## Decision

- Package Windows amd64 with a source-owned NSIS script and Wails-generated build assets from the exact Wails module pinned in `go.mod`.
- Install per user without elevation under the current user's local application directory.
- Bundle the desktop, worker, and CLI from one build. Keep their product version synchronized and verify that invariant in source tests.
- Require Authenticode signatures on all three executables and the installer for release packaging. Resolve the certificate by SHA-1 thumbprint from the current-user certificate store; do not accept a PFX file or password through the packaging interface.
- Require RFC 3161 timestamping and run SignTool verification after every signing operation.
- Check for WebView2 before installation, but never download or execute a runtime bootstrapper from the installer.
- Preserve Vault and user data during upgrade and uninstall. Data deletion is a separate product operation, not an installer side effect.
- Emit a package receipt containing the source commit, artifact hashes, sizes, and Authenticode status. Verify that receipt before release promotion.
- Refuse release packaging from a dirty Git worktree so the receipt's source commit identifies the built source instead of merely naming its nearest ancestor.
- Restrict destructive upgrade-smoke execution to disposable Windows CI roots and require signed old and new installers.
- Keep automatic update disabled. Manual signed distribution and automatic update have different trust and recovery gates.

## Consequences

Developers can validate packaging source and PowerShell syntax without code-signing credentials. A real release still needs a provisioned Windows signing host with NSIS, SignTool, the current-user certificate, and an installed WebView2 runtime for install tests. The package command refuses to overwrite completed release artifacts, which makes retries explicit instead of silently replacing evidence.

The installer intentionally does not make a machine ready by downloading prerequisites. That shifts WebView2 provisioning to documented deployment policy but avoids an opaque executable-download path inside a privileged distribution surface.

## Evidence

- PowerShell AST parsing for package, signing, receipt verification, and upgrade-smoke scripts
- static NSIS tests for per-user scope, companion binaries, WebView2 fail-closed behavior, signing hooks, and Vault retention
- cross-file version synchronization tests
- provisioned-host package receipt verification and signed N-1 upgrade smoke test before release promotion
