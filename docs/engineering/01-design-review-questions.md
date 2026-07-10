# Design Review Questions

- Which aggregate and source of truth owns the state?
- Can the same command arrive twice or resume after an unknown result?
- Which revision makes the decision, evidence, or memory valid?
- What untrusted input crosses the boundary and how is it validated?
- Which capability is needed, for what root/destination, and for how long?
- What content may be persisted, logged, sent to a model, exported, or synced?
- What happens on worker crash, app crash, disk full, database lock, provider timeout, and cancellation?
- Does the change touch ZDP identity/consent without conflating it with Talos content ownership?
- Can the behavior be proven with a fake provider and malicious repository fixture?
- Is rollback a code rollback, data restore, key action, projection rebuild, or user-visible conflict?
