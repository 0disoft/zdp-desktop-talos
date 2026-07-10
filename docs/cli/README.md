# `talosctl`

- Status: Planned companion CLI

`talosctl` is an operator and diagnostic surface for the same application services used by the desktop app. It is not a bypass around the Permission Broker, Vault lock, Decision Queue, or Verification Gate.

Phase 0 implements `talosctl doctor` only. Later commands may inspect Vault/workspace/task status, export a redacted diagnostic receipt, and validate contracts. Mutating commands require the same idempotency, expected revision, capability, audit, and confirmation rules as desktop actions.

The CLI never prints secrets, full environment values, raw provider requests, or repository contents merely because `--json` is selected.
