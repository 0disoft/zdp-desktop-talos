# Product Brief

- Status: Accepted baseline
- Owner: ZDP/Talos product

## Problem

Developers repeatedly explain the same repository rules, preferences, rejected approaches, and verification criteria to coding agents. Ordinary chat history stores conversation but does not reliably turn durable decisions into scoped execution policy.

## Product

Talos Agent is a memory-backed local coding-agent runtime. A user opens one Git repository, submits a high-level task, reviews a Task Contract, and receives an isolated patch with current verification evidence. Decisions that need the user are queued without blocking unrelated safe work. Meaningful outcomes become reviewable memory candidates for later tasks.

## User Promise

Talos remembers only what can improve future work, shows why a memory was applied, and keeps repository data under local control unless the user explicitly approves a disclosed egress path.

## MVP Outcome

One user can complete one task in one repository through contract, plan, scoped execution, Decision Queue, verification, patch review, memory candidate approval, restart recovery, and reuse of an approved memory in a later task.

## Non-goals

Global keystroke capture, screen recording, browser automation, team collaboration, multi-agent swarms, plugin marketplace, real-time relay, vector search, automatic dependency installation, and automatic commit/push/merge are outside MVP.
