# ADR 0033: Redacted Bounded Safe Diff Viewer

- Status: Accepted
- Date: 2026-07-15

## Context

Patch Review could prove freshness and list changed files, but users could not inspect line-level content before a future Apply command. Passing unrestricted `git diff` output into the privileged renderer would expose repository secrets, binary payloads, absolute paths, external diff helpers, text-conversion hooks, and unbounded memory use.

Untracked files are not included in a normal `git diff HEAD`, while temporarily staging them would mutate the patch being reviewed. The viewer therefore needs a read-only path for both tracked and untracked content.

## Decision

- Capture tracked text through system Git with `--no-ext-diff`, `--no-textconv`, fixed context, fixed prefixes, and explicit repository-relative path arguments.
- Read untracked regular files directly without following symbolic links. Generate a bounded added-file view in memory without modifying the Git index.
- Omit binary, symbolic-link, unsupported-type, and excess-file content while preserving safe metadata and reason codes.
- Limit text to 64 KiB per file, 512 KiB total, and 256 content-bearing files. Mark truncated results explicitly.
- Recompute the deterministic worktree state after diff capture. Any concurrent change invalidates the entire review instead of returning torn status, content, and hashes.
- Derive `patch_hash` from the versioned review contract and current state hash. It is an opaque binding identifier, not a content export.
- Pass every text diff through an injected secret-scanner port. The built-in deterministic scanner redacts high-confidence private-key, credential assignment, URL credential, GitHub token, AWS access-key, and OpenAI-style key shapes. Scanner cancellation or failure aborts the review.
- Return only sanitized text, repository-relative paths, line counts, truncation flags, omission reasons, finding counts, and bounded evidence metadata through Wails.

## Consequences

Users can inspect meaningful text changes without granting the renderer arbitrary repository reads. The scanner deliberately favors high-confidence patterns and cannot prove that arbitrary business data is safe; sensitive-source policy and stronger pluggable scanners remain later gates for export and model egress.

Large files and repositories receive partial or metadata-only views rather than silently exceeding the product budget. Apply must still revalidate `patch_hash`, current state, contract scope, decisions, and fresh evidence immediately before mutation.

## Verification

- Git adapter tests cover tracked truncation, untracked text, binary omission, stable state and patch hashes, and payload budgets;
- scanner tests prove representative credential and private-key shapes are removed while unrelated context remains;
- application tests prove raw diff text is sanitized before result construction;
- Wails tests prove only sanitized text and relative paths cross the renderer boundary;
- frontend checking and production builds validate strict response parsing and safe escaped rendering.
