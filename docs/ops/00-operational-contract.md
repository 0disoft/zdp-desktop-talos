# Operational Contract

- Status: Local-first baseline

Talos must remain useful for local review and already-authorized repository work when ZDP account services or model providers are unavailable. Authentication outage, provider outage, worker failure, database failure, and update failure are separate states with separate recovery actions.

No background daemon performs unseen repository, network, Git remote, or package-manager work in MVP. Operations data is local and bounded by default. Remote diagnostics, relay, and telemetry require explicit product contracts and consent before implementation.
