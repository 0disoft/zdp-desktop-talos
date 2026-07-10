# Product Specification

- Status: Accepted MVP baseline
- Owner: ZDP/Talos product
- Related ADRs: `../adr/0001-initial-architecture-boundaries.md`, `../adr/0002-contract-source-of-truth.md`

## Identity and Signup

Talos participates in ZDP shared signup. ZDP core owns account identity, authentication, consent, membership, and account-level audit. Talos stores a stable ZDP account reference and a device-local Vault membership record. Signup or login must not silently transfer repository content, prompts, model responses, terminal output, diffs, memories, or secrets.

Account linkage and data synchronization are separate user actions. Losing network access must not prevent local task review, local memory review, or access to an already-unlocked Vault.

## Core User Flow

1. Open or create a local Vault.
2. Link a ZDP account or continue in an explicitly supported local-only mode once that policy is decided.
3. Open one local Git repository and inspect its state.
4. Create and confirm a versioned Task Contract.
5. Assemble scoped approved memories and repository context.
6. Plan and execute permitted steps in a task worktree.
7. Queue blocking, quality, and follow-up decisions with reason, risk, safe default, and blocked scope.
8. Verify the current diff and present patch evidence.
9. Apply or discard the patch explicitly.
10. Review, approve, reject, or quarantine memory candidates.

## MVP Capabilities

- local Vault creation, lock, unlock, retention settings, and hard-purge workflow;
- one active Run per repository;
- immutable Task Contract revisions with baseline commit and allowed paths;
- capability-based file, process, network, credential, and Git permission decisions;
- restart-safe attempts and idempotent commands;
- current-revision test, type, lint, security, and diff evidence;
- memory lifecycle: candidate, approved, stable, stale, rejected, quarantined, superseded, deprecated;
- Markdown/YAML/JSONL projections that can be regenerated;
- optional manual encrypted Git export/import after redaction and secret scanning.

## Completion Rules

A task cannot be marked complete when required evidence is missing, evidence targets an older revision or diff, a blocking decision remains open for the completion scope, or the patch escaped the Task Contract.

## Privacy Defaults

- telemetry and crash-content upload are off by default;
- model egress is disclosed per workspace/provider and recorded without duplicating secret content;
- `secret` values are never persisted as event payloads or projections;
- private and sensitive content is excluded from Git projection unless an explicit policy permits a safe derivative;
- logical deletion and physical purge are distinct user-visible operations.

## Success Measures

Repeated instruction rate, approved-memory precision, memory contribution rate, incorrect-memory intervention rate, patch adoption, recovery success, and secret-export findings. Evidence-free completion and exported secrets both have a target of zero.
