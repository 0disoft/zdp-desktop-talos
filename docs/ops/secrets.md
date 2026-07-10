# Secrets

Provider keys, ZDP tokens, device private keys, Vault keys, passwords, and repository credentials are secret values. They live in OS-backed stores or encrypted key envelopes and are addressed by opaque handles. Plaintext is not written to events, SQLite rows, logs, crash reports, command output, projections, fixtures, or Git.

Secret detection runs at collection, persistence, logging, model egress, diagnostics, projection, and sync boundaries. Scanner failure blocks export. Rotation changes handles/envelopes without rewriting unrelated history; revocation invalidates future use and produces a non-secret audit event.
