# Observability

Structured local events expose task/run/step/attempt identity, correlation, duration, result code, capability class, provider/model identifier, redaction counts, artifact hashes, and evidence freshness. They exclude raw prompt, code, terminal content, secrets, credential handles that reveal identity, and personal data by default.

Metrics are derived locally and telemetry is opt-in. Debug mode cannot silently disable redaction or expand retention. The UI can explain which memory, permission, decision, and evidence affected an outcome without revealing protected payloads.
