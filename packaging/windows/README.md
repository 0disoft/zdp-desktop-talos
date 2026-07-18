# Windows Packaging

- Status: implemented contract; signing-host execution pending
- Installer: NSIS, current-user scope, Windows amd64
- Product version: `0.19.0`

The package source uses the build-asset generator from the exact Wails module pinned in `go.mod`. Generated Wails assets live only in `.artifacts/`; the repository owns the Talos NSIS overlay and release scripts.

The installer contains `talos-desktop.exe`, `talos-worker.exe`, and `talosctl.exe`. It installs under the current user's Local AppData without elevation, refuses installation when WebView2 is absent, and never downloads a runtime during install. Uninstall removes product binaries and shortcuts but deliberately leaves Vault, WebView profile, diagnostics, and recovery data untouched. Explicit data purge remains an application workflow.

`package.ps1` requires a clean Git worktree and a provisioned Windows packaging host with NSIS. Signed packages additionally require `signtool.exe` and a code-signing certificate already imported into the current user's certificate store. `TALOS_SIGN_CERTIFICATE_SHA1` contains only the certificate thumbprint; no PFX path or password is accepted by the script. Timestamping defaults to DigiCert and can be changed with `TALOS_TIMESTAMP_URL`.

The build emits a versioned JSON receipt containing the source commit, file sizes, SHA-256 digests, and Authenticode status. The clean-worktree gate prevents that commit from being a false label for uncommitted source bytes. `verify-package.ps1` validates the receipt and can fail closed on any unsigned artifact. `upgrade-smoke.ps1` is hard-blocked outside an ephemeral CI account because it installs and removes the real per-user product registration.

Automatic update remains disabled. A signed NSIS installer and a passing N-1 upgrade smoke test are release evidence, not permission to enable unattended update checks.

The dedicated signing-host contract is `signing-host.json` and its fail-closed probe is `verify-signing-host.ps1`. Runner enrollment and GitHub environment setup are documented in `docs/ops/windows-signing-host.md`. The signing host and destructive upgrade-smoke runner are intentionally different machines and labels.
