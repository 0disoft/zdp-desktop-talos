# Installers

- Status: Windows amd64 source contract implemented; signed-host execution pending

The first installer is a per-user NSIS package for Windows amd64. Its source contract is under `packaging/windows/` and bundles `talos-desktop.exe`, `talos-worker.exe`, and `talosctl.exe`. It installs under the current user's local application directory, creates Start Menu and uninstall entries, and never requests administrator elevation.

The package command generates Wails assets from the repository's pinned Wails module, builds the three installed binaries plus a test-only release probe, signs each artifact, builds and signs the NSIS installer, and writes a hash-and-signature receipt. The release probe is published beside the receipt and is never included in the installer. Release packaging fails closed when NSIS, SignTool, the current-user signing certificate, or the required signature is unavailable. The signing interface accepts a certificate thumbprint, not a PFX path or password.

The installer does not download WebView2 or any other executable. It checks the installed WebView2 runtime and exits with a stable failure code when the prerequisite is absent. Provisioning that prerequisite is an operator or user action outside the installer trust boundary.

Upgrade and uninstall preserve the Vault and other user data. The installer owns application binaries and shortcuts only; explicit data deletion remains a separate authenticated product operation. `tools/windows-upgrade-smoke.ts` is restricted to an explicitly disposable Windows CI account. It validates trusted signing-run ancestry and exact receipts, verifies installed binary hashes, creates a real encrypted Vault with the old signed probe, opens and mutates it with the candidate probe, proves uninstall retention, requires the old probe to read the new revision, and purges only that exact test Vault.

Direct distribution remains the initial channel because repository and development-tool execution conflicts with some app-store sandbox models. A generated installer is not release evidence by itself. A release requires the receipt verifier, clean install, signed N-1 upgrade, uninstall-with-data-retention, exact test-Vault purge, and direct rollback-read evidence from the separate disposable upgrade runner. The first release that publishes the signed probe is only a future N-1 baseline; it cannot prove earlier packages retroactively.
