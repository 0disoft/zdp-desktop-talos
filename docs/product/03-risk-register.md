# Risk Register

- Status: Active
- Owner: ZDP/Talos engineering

| Risk | Failure mode | Control | Release gate |
|---|---|---|---|
| Memory pollution | stale or inferred rules keep changing good patches | provenance, scope, conflicts, expiry, explicit gate, evaluation corpus | harmful-memory regression blocks release |
| Sandbox illusion | repository tests read user files outside the worktree | honest capability language, restricted environment, platform sandbox milestone | no claim of OS isolation before proof |
| Prompt injection | README or tool output requests secrets or broader authority | untrusted-content boundary and deterministic broker | malicious-repo fixtures pass |
| Secret persistence | credentials enter ledger, logs, model calls, or projections | redaction at collection/storage/egress/export and fail-closed scanner | any secret fixture leak blocks release |
| Stale evidence | code changes after tests but UI still shows verified | bind evidence to repository revision and diff hash | freshness regression blocks release |
| Duplicate effects | crash causes a command, patch, or answer to run twice | idempotency key and attempt journal | crash-recovery scenarios pass |
| Account/data conflation | shared signup is mistaken for automatic data sharing | separate identity link, consent, egress, and sync contracts | signup privacy scenario passes |
| Wails v3 churn | framework changes leak into domain/application code | isolate transport and pin exact validated version | dependency drift and platform build gate |
| Git history leak | sensitive projection survives deletion | structural export exclusion and encrypted packs | export scanner must pass |
| Permission fatigue | users approve broad dangerous scopes | task/capability grants with expiry and no blanket shell grant | UX and policy review |
| Endless repair loop | model consumes unbounded time and cost | attempt, time, token, and cost budgets | budget exhaustion scenario passes |
