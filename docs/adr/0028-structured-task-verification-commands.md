# ADR 0028: Structured Task Verification Commands

- Status: Accepted
- Date: 2026-07-13

## Context

Completion evidence must be tied to checks declared by the confirmed Task Contract. A shell command string is not a safe contract: quoting is platform-dependent, display text can differ from executed behavior, and passing it to a shell would turn repository or renderer input into command authority. The renderer also cannot own executable paths, inherited environment, timeouts, or output limits.

Existing encrypted contract events predate executable verification and must remain readable. New confirmations, however, need a concrete verification requirement before a Run can claim completion.

## Decision

- Store each verification command as a policy `rule_id`, an exact ordered argument array, and a repository-relative working directory.
- Reject Windows drive, UNC, POSIX absolute, escaping, wildcard, NUL-containing, oversized, or duplicate command definitions in the domain model using host-independent repository path semantics.
- Require at least one verification command at the Vault-session confirmation boundary for new and revised contracts. Continue to decode legacy event payloads whose command collection is absent.
- Keep command bodies in the encrypted Task Contract event. Do not copy them into materialized Task or revision rows.
- Treat the rule ID as a request for trusted runtime policy, not as an executable name. A later execution assembly must resolve the rule to an absolute executable and server-owned environment, timeout, and output limit before the Permission Broker sees a `ProcessIntent`.
- Expose only the structured fields through `TaskService`; do not expose a generic shell, executable, environment, timeout, or output-limit field.

## Consequences

Contract revisions can express reproducible verification without granting execution authority. Different rule catalogs can be assembled per supported toolchain while the stored contract remains provider-neutral. A missing or unknown rule fails closed during execution rather than falling back to a shell.

The current desktop form authors one structured command per confirmation. The domain and transport support bounded command collections so the UI can add repeatable rows without changing storage semantics.

## Verification

- Domain tests reject invalid rule IDs, empty or NUL arguments, Windows drive, UNC, POSIX absolute, escaping and wildcard working directories, and duplicate commands on every host.
- Renderer parser tests prove set-like contract fields deduplicate while verification argument arrays preserve exact order and repeated values.
- SQLite restart tests restore the structured command from the encrypted event payload.
- Wails tests prove the renderer fields reach the Vault application boundary and that an empty verification set is rejected before persistence.
- Frontend checking and production build verify the typed structured request.
