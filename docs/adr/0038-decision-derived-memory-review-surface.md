# ADR 0038: Decision-derived memory compiler and explicit review surface

- Status: Accepted
- Date: 2026-07-17

## Context

The Memory Kernel can persist, gate, and assemble reviewed records, but an encrypted ledger and a candidate API do not produce trustworthy memories by themselves. Letting a model summarize arbitrary events would create a second unreviewed authority path and make retries, provenance, and extraction quality difficult to explain. The desktop also needs a bounded way to inspect candidates and see which approved records affect the current Task.

## Decision

- Compile candidates only from answered Decision records. The current Task, contract revision, Decision, question, and selected answer must all validate and belong to the same open Vault and immutable workspace baseline.
- Derive one workspace-scoped `decision` candidate per answered Decision revision. The statement combines the question with the user-confirmed answer; the rationale identifies the user-confirmed Decision source. The question and answer event IDs are mandatory same-Vault evidence.
- Preflight every eligible Decision through the deterministic secret scanner before writing any candidate. A finding or scanner failure aborts the entire compile request so a secret cannot become later model context and a partial candidate set cannot masquerade as complete.
- Use `memory-decision:<decision-id>:<question-revision>:<answer-id>` as the extraction idempotency key and the answer timestamp as candidate occurrence time. Recompilation returns the current memory state, including an earlier review result, without creating a second candidate.
- Derive at most eight normalized applicability terms from the current Task goal. Candidate creation still stops at `candidate`; extraction cannot approve, stabilize, grant permission, expand scope, or satisfy verification.
- Expose only use-case-specific Wails methods to compile the current Task, list candidates, record an optimistic-revision approval/rejection/quarantine, and explain currently selected memory. No raw event, SQL, generic transition, or arbitrary context-assembly API crosses the renderer boundary.
- Keep review request timestamps server-owned by the store. The renderer supplies an idempotency key but cannot choose event time, and a retry with the same request ID must retain the same command fingerprint.
- Render statement, rationale, confidence, applicability terms, provenance count, and the three review outcomes. Separately render approved memories selected for the current Task with the local applicability reason and revision.

## Consequences

Talos now closes a narrow production memory loop for explicit user Decisions without pretending that arbitrary model-generated summaries are durable knowledge. Candidate review remains user-controlled and selected-memory reasons are visible before planning. Preferences, procedures, failure patterns, environment facts, automatic stable promotion, extraction evaluation, projection, sync conflict handling, and deletion UX remain separate work.

## Verification

- compiler tests cover one-candidate-per-answer idempotency, current-state replay, malformed provenance, cross-Vault rejection, and all-or-nothing secret rejection;
- Wails tests cover compile, list, explicit approval, current-Task explanation, invalid outcomes, and workspace-baseline ownership;
- Svelte type checking and production build validate the strict bounded renderer contract;
- the full Go suite continues to cover encrypted evidence ownership, lifecycle transitions, context budgets, and planning integration.
