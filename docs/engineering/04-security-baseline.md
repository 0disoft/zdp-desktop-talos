# Security Baseline

Security controls are enforced in deterministic code, not prompts. Least-capability, default-deny egress, schema validation, canonical paths, explicit argv, environment allowlists, bounded output, secret exclusion, signed updates, encrypted local data, revision-bound evidence, and audited decisions are baseline requirements.

Renderer-visible patch content must come from an owned worktree, remain repository-relative, disable external Git diff and text-conversion hooks, reject or omit binary and symbolic-link content, enforce per-file and total byte budgets, and pass deterministic secret redaction. A scanner error fails closed. Redaction reduces accidental exposure but is not proof that arbitrary source text is non-sensitive.

Patch apply must revalidate the review hash, verification evidence, contract scope, blocking Decisions, owned worktree, and clean primary baseline immediately before mutation. It may dirty the primary worktree only after an explicit user command and must never commit or push. Patch discard may remove only a marker-verified Talos-owned worktree. Pending or unknown external-mutation journals require manual inspection and cannot be replayed automatically.

Critical or high unresolved vulnerabilities, secret-export findings, invalid update signatures, permission bypass, primary-worktree escape, stale-evidence completion, or unrecoverable migration failure block release. Dependency scans and model safety claims do not replace exploit-oriented fixtures at the actual boundary.
