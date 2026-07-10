# Project Invariants

- local Vault state is authoritative for Talos runtime data;
- shared signup never implies repository or memory synchronization;
- domain and application packages do not import frameworks or concrete adapters;
- the model cannot grant permissions or declare completion;
- the renderer cannot access generic native capabilities;
- repository writes occur in a task worktree, never the primary worktree;
- secret values cannot be event payloads, diagnostics, model input, or projections;
- command side effects are idempotent and revision-sensitive writes use optimistic concurrency;
- evidence targets the current revision and diff;
- projections and indexes are rebuildable;
- imported memories remain untrusted until validation and the Memory Gate;
- automatic commit, push, merge, dependency install, and unrestricted network egress are outside MVP.
