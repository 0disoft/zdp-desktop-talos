# ADR 0039: Explicit OpenAI plan-proposal boundary

- Status: Accepted
- Date: 2026-07-18

## Context

ADR 0036 established a provider-neutral planning core and metadata-only egress receipts, but the desktop had no production provider, credential boundary, disclosure surface, or review-only proposal path. Wiring a hosted provider directly into the existing `Run` method would let one UI action create a plan and immediately begin executing it. That would make the review surface cosmetic and would also couple local Vault availability to provider configuration or quota.

Talos needs one production adapter to prove the boundary without turning a vendor SDK, current model alias, credential value, or provider outage into product-core state.

## Decision

- Add an OpenAI Responses API adapter behind `modelprovider.Provider` using the standard Go HTTP client rather than a provider SDK. The endpoint is fixed to `https://api.openai.com/v1/responses`; product configuration cannot redirect the bearer credential to another host and redirects are never followed.
- Read `OPENAI_API_KEY` only at call time. Do not copy the value into a Wails DTO, Vault event, model receipt, log, command line, child-process environment, or frontend state.
- Require an explicit `TALOS_OPENAI_MODEL` value. No moving model alias is compiled into the product. Invalid or absent configuration disables only model planning and leaves local Vault, Task, Decision, Memory, execution, and patch review available.
- Use Responses Structured Outputs with a strict JSON Schema for the existing bounded `planning.Plan`. Set `store: false`, a derived `max_output_tokens` ceiling, a response-body ceiling, a provider deadline, and the existing application-owned byte and step budgets. Treat refusal, incomplete output, multiple output-text bodies, unknown plan fields, redirects, rate limits, timeouts, and malformed usage as explicit non-execution states.
- Split model runtime behavior into `Propose` and `Run`. `Propose` performs redaction, receipt preparation, provider invocation, strict validation, and receipt completion but never invokes a tool. The desktop exposes only `Propose`; verification execution remains a separate user action through `ExecutionService` and the Permission Broker. `Run` remains an application-level composition used by deterministic fixtures.
- Bind desktop egress confirmation to the exact provider key, model key, canonical workspace root, baseline commit, and Task Contract revision. Any mismatch requires a new confirmation. The disclosure states that only the encrypted Task Contract derivative and applicable approved memories are assembled; arbitrary repository files and terminal output are not sent by this surface.
- Return only a validated plan, safe receipt counters, provider call ID, and the reasons for included memories. Never return the raw request, raw provider response, Authorization header, or credential material.
- Test the adapter with an in-process fake HTTP transport. Do not spend provider quota or require a live key in pull-request or local verification.

## Consequences

The first hosted adapter is usable without making OpenAI a domain dependency. Users see the exact provider/model and approve the current egress scope before every proposal. A proposal cannot execute itself, and provider billing, rate limits, or outages cannot lock local data. The remaining production evidence is a user-authorized live contract smoke on a funded account plus provider-retention and account-policy review; those are external gates, not substitutes for local contract tests.

## Verification

- adapter tests assert fixed-host Authorization, `store: false`, strict schema, token ceiling, redirect refusal, rate-limit mapping, refusal rejection, and unknown-field rejection;
- model-runtime tests prove `Propose` returns without invoking the executor;
- Wails tests prove missing, stale, or mismatched consent cannot construct a runtime;
- Svelte diagnostics verify the disclosure, confirmation, proposal, receipt, and separate execution controls;
- the full Go suite, renderer build, desktop/worker/CLI build, doctor, and strict scaffold validation remain release evidence.
