# ADR 0044: Fail-closed account status surface

- Status: Accepted
- Date: 2026-07-18

## Context

Talos has a contract-tested product-link HTTP adapter, but ZDP core still marks the live product-link handler, consent transaction integration, audit persistence, migration apply evidence, and operational review as blocked. Wiring configurable endpoints into the desktop now would expose a user flow that cannot complete against an approved production authority.

At the same time, the desktop needs an honest user-visible account boundary and a recovery path for a local membership that may already exist after testing or a future downgrade.

## Decision

- Register a bounded Wails `AccountService` that exposes only current local membership status and offline unlink.
- Report `local_only`, `vault_locked`, `unlinked`, or `linked` without returning subject, workspace, consent, link-receipt, session, or credential references to the renderer.
- Keep `link_available` false with `PRODUCT_LINK_UPSTREAM_NOT_READY` until the upstream promotion gates and a production end-to-end review are complete.
- Allow unlink against the encrypted local AccountLink aggregate even when the network verifier is unavailable. Unlink uses the current revision and an idempotency key.
- Show the same boundary in the Svelte shell. Do not render a signup or link action while the production route is blocked.

## Consequences

The UI no longer implies that shared signup is already live, and an existing local link can be removed without ZDP availability. Activating link creation later still requires a stable client-instance identity, reviewed endpoint configuration, system-browser integration, upstream readiness evidence, and end-to-end tests. This ADR does not promote the dormant HTTP adapter or remove any ZDP core blocker.

## Verification

- Wails tests cover locked, unlinked, linked, and offline unlink states;
- DTOs expose only mode, state, availability, reason, and local revision;
- Go tests keep account-store and error mapping behavior aligned;
- `svelte-check` and the production frontend build pass with the local-only account panel.
