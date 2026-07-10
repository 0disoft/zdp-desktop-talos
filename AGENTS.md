# AGENTS.md

## Repository Scope

Scope: general

This repository owns the Talos desktop product, worker, CLI, domain/application core, local Vault adapters, frontend, contracts, tests, and their durable design documents.

The original ssealed documents remain project-owned guidance; application source is now an explicit repository responsibility.

## Repository Shape

- Primary repository type: desktop-app
- Addons: cli-tool

- desktop-app: This repository type owns installed app behavior, OS support, local data, installer, auto-update, crash reporting, permissions, and desktop-specific security contracts.
- cli-tool: This repository type owns command behavior, arguments, flags, config loading, exit codes, terminal output, JSON output, runtime compatibility, and shell integration contracts.


## Source of Truth

- Product scope: docs/product/02-spec.md
- Architecture decisions: docs/adr/*.md
- Runtime implementation: main.go, cmd/, internal/, frontend/
- Dependency declarations: go.mod, frontend/package.json, frontend/bun.lock
- Validation: VALIDATION.md
- Agent routing: .agents/context-map.md
- Repository hygiene: .editorconfig, .gitattributes, .gitignore

## Hard Rules

- Implement only behavior authorized by the product specification and accepted ADRs.
- Do not invent technology choices. Use UNDECIDED when a decision is not known.
- Do not create fake credentials, tokens, secrets, or private values.
- Do not rely on generated, cache, or build output as source truth.
- Keep Wails in the root desktop assembly and transport packages; domain and application packages must not import Wails, SQLite drivers, model SDKs, or Git implementations.
- Treat renderer inputs, repository content, worker frames, model output, paths, and process arguments as untrusted.
- Do not expose generic filesystem, SQL, credential, URL, or shell execution methods through Wails services.

## Repository Hygiene

- .editorconfig sets line ending, encoding, and final newline policy.
- .gitattributes sets Git text normalization and binary diff policy.
- .gitignore excludes local, secret, build, and cache artifacts.
- Generated, cache, and build output must not be used as design-document evidence.
- Do not create large diffs that only change line endings.

## Before Editing

- Read this file, VALIDATION.md, CHECKLIST.md, and .agents/context-map.md.
- Read the skill and checklist named by the context map.
- Confirm source-of-truth documents before changing contracts.

## Out of Scope

- Docker, Kubernetes, Terraform, cloud relay, plugin marketplace, and multi-agent orchestration.
- Project-specific credentials or deployment secrets.
- Automatic commit, push, merge, dependency installation by the product, or unrestricted network egress.

## Final Response Requirements

- List executed validations, passed validations, skipped validations, skip reasons, and remaining risk.
- Name any source-of-truth documents changed.
- Call out API, DB, repository hygiene, and runner changes explicitly.
