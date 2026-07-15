# System Boundary

- Status: Accepted baseline

## Talos-owned

Desktop UI, local Vault, workspace inspection, Task Contract, Run/Step/Attempt lifecycle, Decision Queue, context assembly, model egress control, permission decisions, worker IPC, task worktrees, verification evidence, patch review, memory candidates and records, projections, and manual encrypted sync packs.

## ZDP-owned

Shared signup, account authentication, consent records, platform membership, account/device registration policy, platform audit contracts, and any future relay service. Talos consumes stable identity and consent contracts; it does not copy ZDP core domain logic into the desktop client.

The account-link adapter verifies ZDP challenge/session evidence outside the core and translates it into opaque references. Talos owns only the device-local Vault membership, link lifecycle, encrypted reference snapshot, and local idempotency record. The test fake is not wired into production bootstrap.

## External

Model providers, system Git, operating-system key stores, file systems, package/test tools, and optional MCP servers. All are adapters outside the domain core.

## Explicit exclusions

`zdp-agent-runtime` and `zdp-context-ledger` are platform-control-plane experiments and are not Talos runtime dependencies. Git projections are exchange artifacts, not the database. MCP is an adapter, not product state ownership.

## Trust Zones

```text
Untrusted: user text, repository files, Git history, model output, tool output, MCP output
    -> schema/path/redaction/egress validation
Trusted control plane: task runtime, permission broker, verification gate, memory gate
    -> scoped worker and encrypted local storage
```
