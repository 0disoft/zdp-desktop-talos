# ADR 0036: Bounded model planning and egress receipts

- Status: Accepted
- Date: 2026-07-16

## Context

Talos needs model-generated plans without turning a provider response into execution authority. Repository text, task goals, and future memory content can contain prompt injection or secrets. A provider timeout, malformed plan, retry, or duplicated desktop request must not bypass permission review, duplicate a tool effect, or leave model egress unexplained. No hosted provider or production model has been accepted yet, so the core contract must not encode one vendor's SDK or wire format.

## Decision

- Route planning through one application-owned model runtime and a provider-neutral `modelprovider.Provider` port. Provider names, wire payloads, SDK types, credentials, and transport errors remain adapter concerns.
- Treat Task Contract and repository context blocks as `untrusted_data`. Stable planning instructions are separate from those blocks. Secret-sensitive blocks are rejected; sensitive blocks require an explicit policy; every accepted block passes the secret scanner before egress.
- Apply product-owned limits before the provider call: provider and model keys, prompt version, context item count, encoded input bytes, output bytes, plan steps, tool intents, and provider deadline.
- Persist a private encrypted `prepared` egress receipt before the provider call. The receipt stores only bounded metadata: task and contract identity, provider/model/prompt keys, request and context hashes, byte counts, redaction count, token usage when reported, safe provider-call identity, status, and safe error code. It never stores raw prompt, repository text, model response, or credential material.
- Finish the receipt as `completed` or `failed` with an idempotent command. Provider and schema failures are explicit safe states rather than an implicit retry loop.
- Accept only schema-versioned plans containing unique steps and `verification_command` Tool Intents. A Tool Intent can name only an existing Task Contract verification-command index. The model cannot supply an executable, argv, environment, working root, credential, network destination, capability, or approval.
- Execute accepted intents through the existing execution coordinator. That coordinator reloads the current contract, resolves the command through bootstrap policy, evaluates grants, journals the attempt, and requires fresh verification evidence. A review-required result stops the plan without executing later steps.
- Use the deterministic fixture provider in PR tests. It is not wired into production and never stands in for a hosted provider. A production provider adapter, credential flow, workspace disclosure UI, live contract tests, and vendor-specific retention review require separate accepted work.

## Consequences

Model output remains a proposal and cannot widen the Task Contract or Permission Broker. Egress is attributable without copying protected content into observability. Provider replacement can occur behind the port, while production provider readiness remains honestly incomplete. Schema version 13 adds the materialized egress-receipt table; event payloads remain encrypted and projections remain derivative.

## Verification

- domain tests reject duplicate commands, malformed plans, invalid receipt transitions, and unsafe metadata;
- application tests reject secret sensitivity, out-of-contract command indexes, oversized or malformed output, and permission bypass;
- the deterministic fixture scenario proves redaction, untrusted-context framing, durable receipt completion, provider-to-executor handoff, and review stop behavior;
- SQLite tests prove atomic lifecycle persistence, idempotent replay, conflicting replay rejection, and absence of raw prompt markers in ciphertext storage;
- source-boundary checks continue to keep provider SDKs and SQLite imports out of domain and application packages.
