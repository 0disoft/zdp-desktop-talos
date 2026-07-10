# OS Support

- Status: Phase 0 decision gate

Architecture must remain portable across macOS, Windows, and Linux, but the first supported release platform is selected only after packaging spikes cover code signing, WebView runtime, worker bundling, key-store access, process-tree cancellation, SQLite durability, and update verification.

The intended rollout order is macOS arm64, Windows x64, macOS x64, Linux x64, then additional architectures. This order is a planning default, not a support promise. An OS enters the support matrix only when installer, upgrade, rollback, local-data migration, crash recovery, and malicious-path fixtures pass on native CI or reference hardware.
