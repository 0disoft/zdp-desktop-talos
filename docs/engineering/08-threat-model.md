# Threat Model

- Status: Active baseline

## Protected Assets

Vault keys, provider credentials, repository content, prompts, model responses, diffs, terminal output, memories, decisions, ZDP account references, device signing keys, update trust keys, and verification evidence.

## Primary Adversaries

Malicious repository content, dependency scripts, compromised model/tool providers, prompt injection, hostile MCP servers, local same-user malware, stolen devices, tampered update channels, conflicted sync peers, and accidental operator disclosure.

## Main Abuse Paths

- repository instructions expand task scope or request secret egress;
- model output invents an executable, path, credential, network destination, or verification command outside the current Task Contract;
- symlinks or path normalization escape the task worktree;
- inherited environment exposes credentials to child processes;
- stale evidence or duplicate retries falsely mark completion;
- remote content invokes privileged WebView bindings;
- projection or diagnostics place sensitive data in Git or support systems;
- account linkage is misrepresented as consent to sync content;
- a revoked device submits signed but unauthorized packs.

## Security Gates

Schema validation, path guard, redaction, egress broker, capability broker, worker protocol validation, evidence freshness, memory provenance, signed updates, device membership validation, and fail-closed export scanning. Malicious-repository and crash-recovery fixtures are release-blocking.

Model receipts contain hashes, sizes, counts, provider/model/prompt keys, safe call identity, usage, status, and safe error codes only. Raw model request and response content is not an observability shortcut. The deterministic fixture provider is test-only and cannot be selected by production assembly.
