# Observability

Structured local events expose task/run/step/attempt identity, correlation, duration, result code, capability class, provider/model identifier, redaction counts, artifact hashes, and evidence freshness. They exclude raw prompt, code, terminal content, secrets, credential handles that reveal identity, and personal data by default.

Model egress receipts add prompt version, request/context/response hashes, encoded byte counts, token usage when reported, safe provider-call identity, status, and safe failure code. They do not duplicate context blocks, Task Contract text, repository source, provider credentials, or model output. A fixture-provider success proves control flow only; it is not hosted-provider availability, billing, retention, latency, or quality evidence.

Metrics are derived locally and telemetry is opt-in. Debug mode cannot silently disable redaction or expand retention. The UI can explain which memory, permission, decision, and evidence affected an outcome without revealing protected payloads.
