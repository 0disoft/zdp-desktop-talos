# Runtime Flow

- Status: Accepted baseline

```text
high-level task
  -> immutable Task Contract revision
  -> repository snapshot + approved scoped memories
  -> redaction and model egress receipt
  -> structured plan and Tool Intents
  -> deterministic capability decision
  -> worker execution in task worktree
  -> artifacts and current-diff verification
  -> patch apply/discard decision
  -> memory candidates with provenance
  -> Memory Gate
  -> later Context Pack
```

The desktop confirms a first contract only after reinspecting the canonical repository root. A dirty worktree or changed commit baseline fails before Vault persistence; later execution uses the recorded commit rather than trusting the primary worktree to remain unchanged.

The model runtime is a fixed planning workflow, not a self-authorizing agent loop. It frames Task Contract and repository context as untrusted data, applies sensitivity policy and secret redaction, enforces input/output/step/tool/deadline budgets, and records an encrypted metadata-only egress receipt before calling a provider port. A valid plan may reference only existing verification-command indexes. Every referenced command is resolved again through the deterministic broker; the provider never supplies the executable, argv, worktree root, capability, grant, or approval. PR evidence uses a deterministic fixture provider that is not production wiring.

## Memory Gate and Context Assembly

Extraction produces a candidate, never an approved rule. Candidate creation requires normalized kind, scope, applicability, sensitivity, confidence, source actor, and existing same-Vault evidence event IDs. The full snapshot is encrypted in the ledger while the materialized pointer stores only state, scope hash, confidence, revision, validity, replacement identity, timestamps, and event references. User review moves a candidate to approved, rejected, or quarantined with optimistic concurrency. Stable promotion, stale or deprecated retirement, reapproval, and same-scope supersession remain separate explicit commands; no query or evaluator promotes a record.

The first production compiler is intentionally narrow: it accepts only validated answered Decisions owned by the current Task, Vault, contract revision, and immutable workspace baseline. It preflights every candidate through the deterministic secret scanner before writing any of them, derives one deterministic workspace-scoped candidate from the user-confirmed answer, and binds the question and answer event IDs as provenance. Re-running compilation is idempotent and returns the latest reviewed state instead of manufacturing another candidate. Arbitrary event summarization and model-authored extraction are not enabled.

For a later Task, Context Assembly queries only unexpired approved and stable records in Vault or matching workspace scope and checks expiry again after adapter retrieval. It filters normalized goal and allowed-path terms, applies deterministic ordering plus item and byte budgets, and returns each selected memory with its revision, source reference, and local match reason. Planning prompt `planning.v2` labels these blocks `untrusted_data`: the model may reflect the statement in its plan summary but cannot use memory to invent a command, expand scope, grant permission, or satisfy verification.

The desktop exposes statement, rationale, confidence, applicability terms, provenance count, lifecycle state, validity, and replacement identity through bounded review methods. Approval, rejection, quarantine, stable, stale, reapproval, deprecation, supersession, and expiry sweep are use-case-specific optimistic-revision commands with server-owned validity choices. A separate current-Task view shows only the active memories Context Assembly selected and the local match reason; it never exposes raw event payloads, SQL, or arbitrary transition arguments.

## Projection

The projection compiler consumes validated memory records after Vault decryption but includes only public records in reviewed non-quarantined lifecycle states. It omits Vault identity, workspace paths, raw evidence IDs, and generation time, sorts by memory ID, renders Markdown, YAML, and JSONL from one model, scans each complete file, and fails closed on any finding. File and bundle hashes bind the exact bytes. The Wails surface returns only a bounded preview and explicit completeness; it cannot choose a path, write files, or mutate Git.

## Manual sync pack

The sync codec accepts only a contiguous device sequence range, encrypts the complete event batch with Vault-bound AAD, hashes the ciphertext, derives an immutable pack identity, and signs the canonical manifest with Ed25519. The local exporter recovers one DPAPI-protected signing identity, selects only the explicit path-free Task v2, Decision v2, and Memory v2 allowlist, atomically assigns previously unassigned events to a contiguous local sequence, scans every payload for likely secrets, and stores the exact ready pack under the Vault envelope. A crash before finalization reopens the same preparing batch rather than allocating another range. Workspace-scoped Memory and context assembly use the Vault-bound workspace identifier; device paths remain only in the encrypted local mapping.

The importer resolves the verification key from durable membership, rejects revoked devices, and records the exact verified pack under a second local Vault envelope before application. Ordered replay preserves the remote event identity and device sequence, marks its origin as imported, and applies one pack in one SQLite transaction. Task and Memory revisions use optimistic lifecycle guards. Distinct concurrent Decision answers are merged as a durable conflict instead of last-write-wins. Unsupported, legacy, malformed, or dependency-incomplete events stay encrypted in the canonical ledger and receive a durable quarantine outcome without changing materialized state. Imported events are never re-exported. An imported Task remains unreadable for repository work until its stable workspace identity is bound to a canonical local Git root that contains the Task baseline. Mapping, remapping, and confirmation remain encrypted device-local events. A local membership revocation emits one portable control-plane event whose authority ID must equal the signed pack sender. Replay requires the authority to remain active, applies an optimistic revision to a known matching target, and creates a revoked tombstone for an unknown target so late registration cannot resurrect the key.

The folder exchange adapter publishes an exact ready pack beneath SHA-256-derived Vault and device path segments. It flushes a same-directory staging file and uses a no-clobber hard link for atomic publication; unsupported filesystems fail rather than overwrite. Identical existing bytes are a replay and changed bytes at the same derived identity are a conflict. Enumeration rejects symlinks and malformed entries, enforces per-file, count, and aggregate limits, and sorts fixed-width sequence names before passing bytes to the authoritative importer. The folder never grants membership or sequence authority and imported files remain immutable delivery artifacts.

The Git exchange adapter keeps that same file contract beneath `talos-sync/` but binds delivery to a canonical clean branch. Export begins from a clean committed HEAD, publishes one pack, then requires that exact untracked path to be the sole repository change. The user reviews, commits, and transfers it with external Git tooling. Import begins and ends on the same clean branch and HEAD and accepts only tracked pack paths; ignored or untracked bytes never reach the importer. Talos invokes only read-only Git inspection and tracking commands, with per-invocation long-path support, and never commits, pulls, pushes, merges, changes remotes, or mutates Git configuration.

The desktop renderer reaches this control plane only through bounded Wails use cases. Sync overview is a side-effect-free query and reports whether a local signing identity already exists. A separate explicit initialization command creates or recovers the DPAPI signing identity and local membership. Enrollment, folder exchange, device revocation, and workspace mapping do not expose generic filesystem, Git, key-store, SQL, or event access. Unsigned 64-bit sequences cross the JavaScript boundary as canonical decimal strings so the renderer cannot round a durable cursor.

Enrollment uses a separately transferred 32-byte random bearer capability, not a copied DPAPI blob or account-login token. The source signs and encrypts an expiry-bounded offer containing the Vault key, original Vault creation metadata, retention setting, and source signing membership. The target creates its own DPAPI-protected signing key, registers both devices, and returns a signed encrypted acceptance. The source registers that exact target and completes the issuer journal. Schema 19 stores only package hashes, peer identity, lifecycle state, expiry, and a Vault-encrypted copy of the exact acceptance; Vault keys, transfer secrets, and plaintext packages never enter events. Same-device retries recover the exact acceptance, while a changed offer or second target acceptance conflicts. Schema 23 adds terminal cancellation and expiry. Startup reconciliation materializes elapsed deadlines, cancellation and expiry erase a recipient's recoverable acceptance envelope, and issuer completion requires an active offered state before target registration. An unconsumed copied offer can still be exercised by another offline holder before expiry, so the secret remains a high-value bearer capability and source completion authorizes only one target. Schema 20 adds the device-local workspace mapping journal and stable Task workspace identifiers. Schema 21 emits one path-free Task v2 sync snapshot for each local legacy contract revision without changing its local Task pointer or re-exporting imported v1 events. Schema 22 does the same for workspace-scoped Memory and context assembly. Portable signed revocation converges device denial after peers import the authority pack.

## Decisions

A Decision blocks only named steps or capabilities. Safe unrelated steps continue. Answers carry the question revision and expected repository revision. Concurrent incompatible answers become `conflicted`; last-write-wins is forbidden for security, privacy, license, public API, and deletion choices.

## Failure and Recovery

The control plane journals an attempt before worker execution. A worker crash yields an unknown or failed attempt, not implicit success. Recovery reconciles attempt identity and idempotency keys before retrying. Model/provider failure cannot bypass permission or verification gates. Disk-full, locked database, corrupt IPC, and stale repository state fail closed with actionable state.

## Completion

Verification evidence records the contract revision, command index, baseline commit, capability hash, deterministic task-worktree state hash, zero exit status, and execution timestamps. The successful Attempt transition and evidence row commit atomically. Raw stdout and stderr are not persisted until redaction and secret scanning own that path. Any later patch change produces a different state hash and invalidates affected evidence before completion is reevaluated.

The desktop execution surface accepts only a Task ID and verification-command index. The coordinator reloads the current Task and contract revision, resolves the stored rule through bootstrap-owned tool policy, and only then evaluates permission and dispatches an exact worker capability. Unknown rules, stale indexes, unavailable tools, and mismatched policy fail before worktree or worker side effects.

The first execution creates a detached Talos-owned task worktree. Later verification reopens that retained patch only after the owner marker, canonical paths, Git top level, and immutable baseline HEAD all match. A directory that cannot prove ownership is neither executed nor deleted automatically.

Patch Review reopens the same owned worktree and returns only a bounded Git status summary: repository-relative paths, change kinds, index/worktree status, and the deterministic current-state hash. It compares the newest successful evidence with the current Task contract revision, immutable baseline, and current-state hash. Any mismatch is `stale`; missing evidence is `unverified`. Raw diff content remains outside the renderer boundary until redaction and payload limits own that path.

The Safe Diff surface reads tracked changes through system Git with external diff and text-conversion hooks disabled, and reads untracked regular files without following symlinks. It computes the status, bounded diff set, and final state snapshot in one fail-closed review operation. Binary, symbolic-link, unsupported, excess-file, per-file, and total-payload cases return metadata rather than unrestricted content. A deterministic scanner redacts high-confidence credential and private-key shapes before text crosses the Wails boundary; scanner failure aborts the review.

Apply and discard are durable commands, not Patch Review side effects. Talos records a pending action before Git mutation and never automatically repeats an unresolved action after restart. Apply reloads the current Task and contract, requires fresh evidence and zero secret findings, checks every changed path and rename origin against the allowed scope, and rejects unresolved blocking Decisions. The primary worktree must still be clean at the immutable baseline. A temporary alternate Git index captures tracked and untracked task-worktree changes into a binary patch without changing the task worktree index; `git apply --check` precedes the real apply. Talos does not commit or push. Discard verifies the current patch hash and owned marker before removing only the task worktree.

Successful action state and the `completed` or `discarded` Task projection commit atomically. A deterministic preflight failure leaves the Task contracted. A failure after external mutation becomes `unknown` and requires direct repository inspection rather than an automatic retry.
