# Desktop Contract

The desktop application is the primary review and control surface for Vault state, workspaces, Task Contracts, Run timelines, Decisions, patch evidence, memory candidates, sync, and privacy settings. The current alpha surface manages protected local Vaults, inspects a Git workspace, confirms revisioned Task Contracts, and lists or answers persisted Decisions without exposing generic storage methods.

The renderer is low authority. It requests application use cases through narrow bindings and re-queries authoritative state after notifications. It cannot execute arbitrary commands, read arbitrary paths, query raw SQL, or access credentials. Platform support, installers, updates, crash handling, local data, and desktop security are specified in this directory.
