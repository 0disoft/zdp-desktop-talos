# Contributing

This is a private ZDP product repository. Changes must preserve the product contract in `docs/product/02-spec.md`, the accepted ADRs, and the validation contract.

Before editing, read `AGENTS.md`, `VALIDATION.md`, `CHECKLIST.md`, and the routed skill/checklist. Keep changes narrow, preserve unrelated work, and state which source of truth authorizes a contract change.

Pull requests must include changed behavior and boundaries, executed and skipped validation with reasons, local-data or migration impact, security/privacy impact, rollback or recovery behavior, and any follow-up decision. A green UI path cannot substitute for domain, storage, permission, evidence-freshness, or crash-recovery tests.

Do not commit credentials, diagnostic payloads, Vault data, private repository fixtures, generated bindings without their source, build output, or model-provider responses.
