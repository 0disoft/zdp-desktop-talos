# Operability and Failure Standard

Every long-running action has identity, phase, bounded progress, timeout, cancellation, and a recoverable terminal state. Unknown is distinct from failed and succeeded. Retries reuse the logical idempotency key and respect attempt/time/token/cost budgets.

Local diagnostics use structured codes and correlation identifiers without raw sensitive content. Health views distinguish Vault lock, database, worker, Git, key store, model provider, egress policy, and update trust. Recovery runbooks must state whether an action resumes, retries, reconciles, rebuilds, restores, or requires user choice.
