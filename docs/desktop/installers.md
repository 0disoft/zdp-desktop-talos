# Installers

- Status: Windows amd64 source contract implemented; signed-host execution pending

The first installer is a per-user NSIS package for Windows amd64. Its source contract is under `packaging/windows/` and bundles `talos-desktop.exe`, `talos-worker.exe`, and `talosctl.exe`. It installs under the current user's local application directory, creates Start Menu and uninstall entries, and never requests administrator elevation.

The package command generates Wails assets from the repository's pinned Wails module, builds all three binaries, signs each binary, builds the NSIS installer, signs the installer, and writes a hash-and-signature receipt. Release packaging fails closed when NSIS, SignTool, the current-user signing certificate, or the required signature is unavailable. The signing interface accepts a certificate thumbprint, not a PFX path or password.

The installer does not download WebView2 or any other executable. It checks the installed WebView2 runtime and exits with a stable failure code when the prerequisite is absent. Provisioning that prerequisite is an operator or user action outside the installer trust boundary.

Upgrade and uninstall preserve the Vault and other user data. The installer owns application binaries and shortcuts only; explicit data deletion remains a separate authenticated product operation. `upgrade-smoke.ps1` verifies retention but refuses to run outside a disposable Windows CI directory.

Direct distribution remains the initial channel because repository and development-tool execution conflicts with some app-store sandbox models. A generated installer is not release evidence by itself. A release requires the receipt verifier, clean install, signed N-1 upgrade, uninstall-with-data-retention, explicit data-removal, and rollback evidence from a provisioned signing host.
