# ADR 0064: Evidence-gated post-MVP extraction sequence

- Status: Accepted
- Date: 2026-09-01
- Owners: ZDP/Talos product, engineering, and platform operations

## Context

Talos already has a local encrypted Vault, signed immutable sync packs, manual folder and Git exchange, explicit capability review, and a deterministic Memory evaluation baseline. Relay delivery, a public protocol, third-party integrations, and vector retrieval are still deferred.

Creating repositories or services because those domain nouns exist would turn internal implementation details into compatibility obligations before product demand and trust boundaries are proven. It would also let infrastructure become accidental authority over the local Vault or hide retrieval defects behind a new index.

A sequence is required because each later extraction depends on a narrower, tested contract from the preceding stage. The public protocol must be extracted before a relay can become a supported service, and integrations must consume that protocol and the existing capability boundary instead of reaching into Talos internals.

## Decision

### Global boundary

- The encrypted local Vault and its SQLite ledger remain canonical product state. Git, relay storage, protocol fixtures, integration processes, and vector indexes are carriers or derivatives, never alternate authorities.
- No new repository, deployable service, or public SDK is created until its stage has a named owner, a concrete consumer, accepted threat and privacy boundaries, measurable entry evidence, rollback ownership, and an explicit stop condition.
- The post-MVP order is: public protocol extraction, private opaque relay, out-of-process integrations, then evidence-gated vector retrieval.
- Passing one stage does not automatically start the next. Product demand and operational ownership are required at every transition.
- The private-alpha distribution, native install and upgrade, hosted CI, backup, and security gates remain independent release requirements. Architecture extraction cannot be used to bypass them.

### Shared data, trust, and compatibility rules

- Talos engineering owns canonical portable schemas, encoders, decoders, limits, and conformance fixtures. Platform operations owns relay deployment, retention, deletion handling, abuse controls, observability, rollback, and incident response. Each integration has an explicit adapter owner. Memory evaluation remains owned by Talos product and engineering.
- The desktop control plane is the authority that decrypts Vault content and grants capabilities. The relay is an untrusted ciphertext carrier. Integration processes are untrusted external-effect adapters. A vector index is a disposable local derivative.
- Every external envelope has an explicit immutable protocol version. Incompatible changes use a new version and an explicit bridge or rejection path; readers fail closed on unknown versions. Database schema numbers, encrypted event layout, prompts, ranking internals, and renderer DTOs are not public compatibility surfaces.
- Retry and recovery use immutable identifiers, bounded idempotency, and local journals. A relay acknowledgement, integration response, or vector hit is not proof that canonical state changed.
- Logs and metrics exclude Vault plaintext, model prompts, repository content, credentials, recovery material, and decrypted pack payloads. Cross-boundary observability is limited to non-secret identifiers, hashes where safe, byte counts, versions, timing, result classes, retries, expiry, and deletion state.
- Deletion claims are scoped to the component that owns the bytes. No stage may promise erasure from user clones, Git history, external backups, third-party systems, or already delivered packs.

## Stage 1: Extract the public protocol

### Scope

Extract only stable external schemas and deterministic conformance material required to exchange encrypted packs, identify supported versions, express bounded capabilities, and classify portable outcomes. The surface excludes SQLite tables, migration internals, prompts, model-provider payloads, retrieval ranking, local filesystem paths, Wails methods, and renderer DTOs.

### Start gate

- The MVP product boundary is accepted and the portable contract has canonical byte fixtures, strict size and unknown-field behavior, deterministic round trips, and an identified external consumer.
- No planned MVP feature requires changing the candidate surface through an internal shortcut.
- Security review confirms that publishing the schema does not expose secrets or imply that protocol possession grants Vault authority.

### Exit evidence

- Versioned schemas, normative encoding rules, bounded decoders, conformance fixtures, negative fixtures, compatibility rules, and a reference validation command are reviewed together.
- At least two independent implementations or one implementation plus an isolated conformance harness produce and reject the same canonical cases.
- Unsupported versions, malformed lengths, unknown fields, invalid signatures, ciphertext tampering, replay identities, and resource ceilings fail closed.
- The protocol can be consumed without importing Talos database, UI, model, or application packages.

### Stop or defer

Defer extraction when the only consumer is Talos itself, when the contract still changes with ordinary feature work, when conformance requires internal database or prompt knowledge, or when a stable compatibility owner is absent.

## Stage 2: Deploy the private opaque relay

### Scope

The relay stores and forwards immutable encrypted protocol packs. It may own opaque routing tokens, pack identity, ciphertext hash, byte length, expiry, delivery state, and bounded abuse metadata. It never receives a Vault root key, decrypted event, repository plaintext, model prompt, memory statement, task execution request, integration credential, or generic query capability.

The relay does not search plaintext, compile memories, resolve conflicts, authorize task execution, mutate Vault state, or become a recovery escrow. The receiving Talos client still verifies signatures, membership, sequence, revocation, version, and replay before local application.

### Start gate

- Stage 1 has exited with a reviewed protocol and compatibility owner.
- A real multi-device delivery need cannot be met adequately by the existing folder or explicit Git exchange.
- Retention, regional handling, account-routing separation, abuse limits, cost ceilings, incident response, and deletion wording are approved before deployment.

### Exit evidence

- Two independently enrolled Vaults prove bounded idempotent upload, retry, download, acknowledgement, duplicate handling, expiry, deletion request handling, outage recovery, and client-side replay validation without relay decryption.
- Relay compromise tests show that stored bytes and telemetry reveal no Vault plaintext, root key, device-signing private key, workspace path, memory statement, or task payload.
- Deployment has health checks, redacted metrics, quota and payload limits, backup policy for ciphertext metadata, rollback, key and credential rotation, and an exercised incident runbook.
- Relay unavailability leaves local Vault operations usable and does not corrupt local sequence or validation journals.

### Stop or defer

Stop or redesign if delivery requires plaintext inspection, generic server-side search, canonical conflict resolution, task execution, root-key custody, unlimited retention, unverifiable deletion claims, or operating cost without demonstrated product demand.

## Stage 3: Add out-of-process integrations

### Scope

Integrations consume the public protocol and explicit capability contracts through a separate adapter process or service boundary. They receive only the minimum reviewed input for one operation and return a bounded typed result. They never open the Vault database, receive the Vault root key, import Talos internal packages, or gain generic filesystem, network, credential, SQL, URL-fetch, or shell authority.

External side effects remain permission-bound Tool Intents with explicit scopes, redaction, retries, idempotency, cancellation, and audit. Integration credentials are owned by the adapter's credential boundary, not copied into Vault events or renderer state.

### Start gate

- Stages 1 and 2 have stable compatibility and operational ownership, or the integration can prove it needs only Stage 1 and no relay dependency.
- One named integration has concrete user demand, a bounded capability map, a credential owner, a revocation path, and a failure budget.
- The adapter can operate without direct database access or hidden background side effects.

### Exit evidence

- Contract tests cover version negotiation, least-privilege scopes, malformed and oversized input, credential absence, redaction, retry safety, duplicate requests, timeout, cancellation, revocation, and adapter crash recovery.
- Process or service isolation proves that adapter failure cannot corrupt the Vault, bypass permission review, or expose unrelated task and memory content.
- Users can disable and revoke the adapter, inspect its last bounded outcomes, and recover from an unavailable or incompatible version without blocking local Talos use.
- Deployment, observability, support ownership, data retention, third-party deletion limits, and rollback are documented for that integration rather than inherited from a generic marketplace promise.

### Stop or defer

Reject an integration that needs broad credentials, direct Vault or SQLite access, silent polling, generic command execution, unbounded content export, hidden account linking, or compatibility commitments without an owner.

## Stage 4: Evaluate vector retrieval

### Scope

Vector retrieval remains inside the Talos modular monolith behind a provider-neutral local port until a separate deployment need is proven. Its index is disposable derived state. The encrypted Memory ledger, lifecycle, expiry, supersession, scope, provenance, permission policy, and deterministic Context Assembly filters remain authoritative.

Vector search may propose a bounded candidate set only after active-memory eligibility is established, or the returned set must be rechecked through the same authoritative eligibility filter before use. It cannot promote memories, override stale or forbidden exclusions, widen scope, or become evidence for a permission grant.

### Start gate

- The existing versioned synthetic corpus and deterministic evaluator are current and repeatable with zero forbidden interventions.
- A measured retrieval deficiency names the affected cases and target metric; infrastructure curiosity is not sufficient.
- Privacy, local storage, model or embedding version, rebuild cost, resource ceilings, and deletion behavior are documented.

### Exit evidence

- Repeated evaluation shows a reviewed material improvement in the named recall or latency target without reducing required precision, introducing forbidden or stale interventions, destabilizing ordering, or violating the bounded result size.
- Index corruption, absence, incompatible embedding versions, interrupted rebuild, and provider failure fall back to the deterministic path without changing canonical Memory state.
- Index creation, replacement, expiry handling, supersession handling, and deletion are bounded and rebuildable from authorized active records. Production content is not copied into test fixtures or telemetry.
- The chosen design records the embedding and index version needed for reproducibility and supports complete local derivative deletion without claiming deletion of external provider data unless that provider contract proves it.

### Stop or defer

Do not ship vector retrieval when improvement is immaterial, forbidden or stale interventions increase, deterministic fallback is weaker, resource cost exceeds the product budget, privacy cannot be bounded, or the design attempts to make the index authoritative.

## Consequences

Talos keeps one product repository and one local source of truth until extraction has a consumer and evidence. Publishing the protocol before deploying relay prevents an implementation-specific service from becoming an undocumented compatibility contract. Relay and integrations remain narrow untrusted boundaries, while vector retrieval must beat the existing deterministic baseline instead of replacing its safety rules.

This sequence deliberately sacrifices speculative parallelism. It avoids four premature repositories, duplicated domain logic, hidden authority shifts, and compatibility debt that would otherwise slow the MVP more than the extraction helps.

## Verification

- The product roadmap names the ordered post-MVP sequence and points to this ADR.
- Each stage defines owners, canonical data, trust zones, versioning, compatibility, recovery, deletion, observability, deployment, measurable start evidence, exit evidence, and stop conditions.
- No runtime, API, database, renderer, credential, runner, deployment, or repository split is created by this decision.
