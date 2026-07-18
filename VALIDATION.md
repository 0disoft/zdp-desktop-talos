# Validation

- Status: Active

## Validation Source of Truth

This document owns stable validation names for this scaffold.

## Standard Validation Names

- format
- lint
- typecheck
- test
- contract
- migration-check
- smoke
- docs
- check

## Required Final Report

Final responses must list executed validations, passed validations, skipped validations, skip reasons, and remaining risk.

## Runner Policy

Task runner files are optional. Runner `none` means no executable task runner is generated.
If a runner is generated, runner command names must match this document.
Unconfigured runner commands must fail, not pass with a fake success.

## Hygiene Validation

Repository hygiene file changes must check line-ending churn, binary diff pollution,
tracked secret files, ignored build/cache artifacts, and generated-output drift.

## Scope

The Taskfile defines repository-local commands. Agents still execute them only through configured workspace mustflow intents.

## Repository Shape

Desktop and CLI validation cover Go tests, Svelte diagnostics/build, Bun-tested Windows release tooling, encrypted storage restart, IPC framing, worker handshake, and binary compilation. Installer signing, installed-package execution, and packaged WebView startup remain release-only manual gates.
