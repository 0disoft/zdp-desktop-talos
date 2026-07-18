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

Do not install release candidates on the signing host. Register a different disposable Windows runner with the `talos-upgrade-smoke` label. Its current-user profile must be throwaway and contain WebView2 but no signing key or pre-existing Talos installation/data. Before any signing-run lookup or artifact download, `verify-upgrade-runner.ps1` enforces the pinned runner contract, verifies that neither the current-user nor local-machine personal certificate store exposes a code-signing private key, checks WebView2 and a clean Talos profile, and writes a no-clobber path-free preflight report beneath `RUNNER_TEMP`. `Windows Upgrade Smoke` then resolves both supplied run IDs as successful manual `main` executions of the signing workflow, verifies old-to-new-to-current-verifier commit ancestry, downloads complete package artifacts, and checks all receipt hashes and Authenticode signatures against `TALOS_EXPECTED_SIGNER_SHA1`.

The signed package-only probe creates and backs up a real encrypted Vault with the old release, opens and mutates it with the candidate, proves candidate uninstall retention, then requires the reinstalled old probe to read the candidate revision before exact Vault purge. The workflow uploads a path-free `talos.windows-upgrade-evidence/1` artifact that embeds the validated preflight facts and binds them to the same expected public signer thumbprint used for both packages. The environment flag and report do not prove that the VM was actually destroyed after the job; runner lifecycle and profile disposal remain operator-owned evidence. A pass proves only the exercised signed version pair; it does not authorize automatic updates or cover versions that do not publish the probe.

Both workflows retain their uploaded package or evidence artifact for 90 days. Schedule the candidate N-1 run before the baseline expires. Do not keep signed packages, Vault test data, or the signing key in a persistent runner workspace as an informal archive.

## References

- GitHub deployment environments and plan-dependent protection rules: <https://docs.github.com/en/actions/reference/workflows-and-actions/deployments-and-environments>
- GitHub secure use guidance for self-hosted runners: <https://docs.github.com/en/actions/security-guides/security-hardening-for-github-actions#hardening-for-self-hosted-runners>
