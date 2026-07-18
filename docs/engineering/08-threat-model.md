# Threat Model

- Status: Active baseline

## Protected Assets

Vault keys, provider credentials, repository content, prompts, model responses, diffs, terminal output, memories, decisions, ZDP account references, device signing keys, update trust keys, and verification evidence.

## Primary Adversaries

Malicious repository content, dependency scripts, compromised model/tool providers, prompt injection, hostile MCP servers, local same-user malware, stolen devices, tampered update channels, conflicted sync peers, and accidental operator disclosure.

## Main Abuse Paths

- repository instructions expand task scope or request secret egress;
- model output invents an executable, path, credential, network destination, or verification command outside the current Task Contract;
- a model, repository file, imported pack, or cross-Vault event attempts to create or approve poisoned memory without valid provenance;
- stale, rejected, quarantined, superseded, or deprecated memory re-enters a later context pack;
- symlinks or path normalization escape the task worktree;
- inherited environment exposes credentials to child processes;
- stale evidence or duplicate retries falsely mark completion;
- remote content invokes privileged WebView bindings;
- projection or diagnostics place sensitive data in Git or support systems;
- account linkage is misrepresented as consent to sync content;
- a revoked device submits signed but unauthorized packs.
- a copied, logged, or shoulder-surfed enrollment capability is exercised on another offline device before expiry.

## Security Gates

Schema validation, path guard, redaction, egress broker, capability broker, worker protocol validation, evidence freshness, memory provenance, signed updates, device membership validation, and fail-closed export scanning. Malicious-repository and crash-recovery fixtures are release-blocking.

Enrollment packages use a random 32-byte bearer capability, authenticated encryption, source/target signatures, bounded size and validity, durable local reuse detection, and exact acceptance recovery. Offline global single use cannot be proven before peers converge: the source therefore completes only one target, the secret must never enter logs or Vault events, and later renderer/file handling must preserve that separation.

Model receipts contain hashes, sizes, counts, provider/model/prompt keys, safe call identity, usage, status, and safe error codes only. Raw model request and response content is not an observability shortcut. The deterministic fixture provider is test-only and cannot be selected by production assembly.

Memory candidates cannot approve themselves. Same-Vault evidence checks, encrypted payloads, optimistic lifecycle revisions, closed terminal states, scope hashes, applicability filters, and context budgets form the Memory Gate. Selected statements remain untrusted model data and never bypass Task Contract, permission, or verification code.
