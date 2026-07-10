# Shell Completion

- Status: Deferred

Shell completion is added only after the command tree is stable. Generated completion must never enumerate Vault names, repository paths, task goals, credential handles, memory text, or remote account data. Completion scripts are versioned with `talosctl` and generated from the command contract rather than maintained as independent handwritten behavior.
