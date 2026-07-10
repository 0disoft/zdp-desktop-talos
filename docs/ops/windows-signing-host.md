# Windows Signing Host

- Status: Source configuration implemented; runner enrollment pending
- Trust boundary: dedicated self-hosted Windows amd64 runner
- Workflow: `.github/workflows/windows-signing.yml`

## Host Contract

The signing host is not a general development machine. Assign the labels `self-hosted`, `windows`, `x64`, and `talos-signing` only to a dedicated runner account. Install the exact Go and Bun tracks from `packaging/windows/signing-host.json`, NSIS 3.x, a current Windows SDK SignTool, Git, and WebView2. Keep the GitHub Actions runner current because pinned JavaScript actions can raise their required runner runtime.

Import the code-signing certificate and private key into `Cert:\CurrentUser\My` for the runner account. Grant private-key access only to that account. Do not store a PFX, private key, certificate password, Vault key, provider key, or repository credential in the repository or workflow. Set `TALOS_SIGN_CERTIFICATE_SHA1` as a GitHub Actions variable. `TALOS_SIGNTOOL_PATH` and `TALOS_TIMESTAMP_URL` are optional variables.

The host preflight validates the OS and architecture, toolchain, certificate location, private-key access, code-signing EKU, expiry window, HTTPS timestamp URL, and clean worktree before dependency restoration or signing begins.

## GitHub Configuration

1. Register the runner at repository or tightly scoped organization level with the required labels. Do not attach the signing label to ordinary self-hosted runners.
2. Create an environment named `windows-signing` and restrict deployment branches to `main` where the current GitHub plan supports it.
3. Configure required reviewers and prevent self-review when the current GitHub plan supports those protection rules. GitHub Free/Pro/Team can limit required reviewers for private repositories, so manual dispatch plus the `main` job condition remains mandatory and must not be described as two-person approval by default.
4. Replace the placeholder in `.github/CODEOWNERS`, protect `main`, and require review for `.github/workflows/**`, `packaging/windows/**`, and release policy changes before enrolling the runner.
5. Set the repository or environment variables described above. Also set repository variable `TALOS_EXPECTED_SIGNER_SHA1` to the same public certificate thumbprint for the upgrade runner. No signing secret value is required by either workflow because the private key remains on the signing host.
6. Run `Windows Signed Package` manually from `main`. Verify the uploaded receipt before treating the artifact as release evidence.

The workflow has read-only repository permission, does not persist checkout credentials, refuses non-`main` dispatches, serializes signing jobs, and pins every external action to a full commit SHA.

## Upgrade Runner

Do not install release candidates on the signing host. Register a different disposable Windows runner with the `talos-upgrade-smoke` label. Its current-user profile must be throwaway and contain WebView2 but no signing key. `Windows Upgrade Smoke` downloads two already-signed workflow artifacts by run ID, verifies that their Authenticode signatures are valid and match `TALOS_EXPECTED_SIGNER_SHA1`, performs the N-1 upgrade, verifies Vault retention, uninstalls, and then discards the runner profile.

An upgrade-smoke pass proves installer and Vault-retention behavior. It does not prove database downgrade compatibility or authorize automatic updates.

## References

- GitHub deployment environments and plan-dependent protection rules: <https://docs.github.com/en/actions/reference/workflows-and-actions/deployments-and-environments>
- GitHub secure use guidance for self-hosted runners: <https://docs.github.com/en/actions/security-guides/security-hardening-for-github-actions#hardening-for-self-hosted-runners>
