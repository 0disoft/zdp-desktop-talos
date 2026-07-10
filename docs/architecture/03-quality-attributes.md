# Quality Attributes

- Status: Initial engineering budget

| Attribute | Initial target |
|---|---|
| Vault startup | 100,000 events without full replay in 2 seconds on reference hardware |
| Event append | local SSD p95 at or below 20 ms |
| Context assembly | model-excluded p95 at or below 250 ms |
| Timeline first page | at or below 300 ms |
| Worker cancellation | process tree ends within 5 seconds |
| Projection rebuild | 100,000 events within 30 seconds |
| Command output | default bounded to 1 MiB |
| Secret export | zero accepted findings |
| Evidence-free completion | zero |

These are engineering budgets, not public promises. Reference hardware and measurement fixtures must be recorded before Alpha.

Maintainability requires inward dependency direction and versioned external contracts. Security requires least capability, explicit egress, secret exclusion, signed updates, and honest separation between policy isolation and OS sandboxing. Recoverability requires idempotent effects, WAL-aware storage tests, encrypted backups, and observable stale states.
