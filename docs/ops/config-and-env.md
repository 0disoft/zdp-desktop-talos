# Configuration and Environment

Configuration is scoped to application, Vault, workspace, task, and provider. Lower scopes may narrow but cannot silently override product security policy. Child processes receive a freshly constructed allowlisted environment, never the full desktop-process environment.

Paths are canonicalized and resolved against an owned root. Configuration values affecting egress, credentials, retention, deletion, signing, update channel, or capabilities are validated and surfaced to the user. Unknown configuration is rejected or quarantined with a version error.
