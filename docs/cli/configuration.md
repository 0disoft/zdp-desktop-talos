# CLI Configuration

- Status: Baseline

Configuration precedence is explicit flags, approved task/workspace policy, Vault settings, then application defaults. Environment variables may select non-secret diagnostic behavior but must not silently broaden capabilities, egress, retention, or filesystem scope.

Secrets are referenced through credential handles backed by the OS key store. Config files never contain provider-key plaintext. Unknown keys, invalid enum values, paths outside allowed roots, and insecure permission broadening fail validation instead of being ignored.
