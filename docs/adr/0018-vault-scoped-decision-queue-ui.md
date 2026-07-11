# ADR 0018: Vault-scoped Decision Queue UI

- Status: Accepted
- Date: 2026-07-11

## Context

The desktop needs to display and answer actual persisted Decisions without exposing generic storage methods or trusting renderer-supplied repository revisions. Decision contents must disappear from the renderer when Vault authority is lost.

## Decision

- Expose use-case-specific Wails methods to create, list, and answer Decisions. Runtime components may create questions; the user UI lists and answers them but does not invent fake runtime questions.
- Scope listing to the open Vault and selected Task with a hard limit of 256 items.
- Reinspect the canonical workspace before creation and answer. A changed commit baseline fails; a dirty worktree with the same commit may still answer because answer validity binds to the commit revision rather than primary-worktree dirtiness.
- Ignore any renderer attempt to choose the authoritative repository revision. The main process supplies the reinspected commit.
- Render reason, unanswered risk, safe default, options, free-form answer, category, state, and question revision.
- Clear Task input, Decision contents, and answer drafts when the Vault locks, is purged, or enters an unknown purge state.

## Consequences

Decision review remains local and tied to Vault authority. The queue can show `conflicted` state but does not silently choose a winner. Explicit conflict resolution and question supersession remain separate transitions required before Phase 3 completion.

## Verification

- Wails create/list/answer tests prove current-baseline and Vault scoping.
- changed commits are rejected before answer persistence;
- dirty same-commit worktrees remain answerable;
- Svelte validation and production build cover bounded response parsing and queue controls;
- lock and purge paths clear private renderer state.
