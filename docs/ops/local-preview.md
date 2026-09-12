# Isolated local desktop preview

The workspace intent `zdp_desktop_talos_preview_build` builds an unsigned desktop,
worker and preview launcher under `.tmp/native-preview` in the review worktree.
Open `talos-preview.exe`, not the sibling desktop binary, for this workflow.
The launcher keeps Vault discovery, DPAPI-encrypted files, WebView data and temporary
files under that bundle's `profile` directory. Provider and repository credentials
are not inherited. Closing the desktop ends the launcher; a forgotten session is
terminated after 15 minutes. Relaunching the same bundle retains its test Vaults.

Use only a disposable local Git repository and synthetic task content. Do not open
a production Vault or approve network access during UI checks. This is profile
separation, not an OS sandbox: filesystem access and current-user DPAPI authority
are not restricted. No installer, signature, update or release readiness claim is
made, and this launcher is not included in the signed package.

Check create/open/lock, task selection, page navigation, empty search continuation,
read-only old baselines, partial progress recovery and window restart. Existing
backend revision, permission and patch freshness gates remain mandatory. Installer
and N-1 upgrade checks still require their dedicated runners.

The bundle's `preview-diagnostic.jsonl` is replaced on each launch and contains
at most three lifecycle records: launcher start, child start and exit/failure.
Only stage names, elapsed milliseconds, exit code and timeout state are recorded;
paths, environment, raw errors and child output are excluded.
`app_started` means process creation, not a rendered or responsive window.
A final `app_exited` record distinguishes a completed child process from a window
discovery failure; a missing final record can also mean the launcher was terminated.
Failures before opening the diagnostic file cannot be recorded there.
