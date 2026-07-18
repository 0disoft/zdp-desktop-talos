# ADR 0051: Signed cross-device device revocation

- Status: Accepted
- Date: 2026-07-18

## Context

Local membership revocation stopped one Vault installation from accepting a device, but the transition was device-local. Another enrolled installation could continue trusting the same signing key until a human repeated the revocation there. That split-brain trust state is unsafe for manual exchange: a lost or retired device must not remain authorized merely because a peer has not reconstructed an out-of-band local command.

A revocation event is security-sensitive control-plane data. It cannot be accepted solely because its JSON names an authority device, and an unknown target cannot be ignored because the target may attempt enrollment after the revocation pack arrives.

## Decision

- Emit `sync.device.revocation.created` schema 1 as a portable sync event when an active local authority revokes a target membership.
- Bind the payload authority device ID to the device that signed the containing pack. A mismatch is quarantined and cannot mutate membership.
- Require the authority membership to be active at replay time. A revoked or unknown authority cannot revoke another device.
- Include the exact target public key, expected target revision, and revocation timestamp in the signed encrypted payload.
- Revoke a known active target only when its public key and expected revision match. Preserve its local import cursor while incrementing the membership revision.
- Treat an equivalent already-revoked target as idempotently applied. A changed key, incompatible revision, or malformed timestamp fails closed.
- Materialize an unknown target as a revoked tombstone. A later attempt to register that device and key is rejected instead of silently resurrecting trust.
- Keep imported events non-exportable. The portable revocation is forwarded only by the originating authority, so one signed decision does not become an unauditable gossip rewrite.
- A local device whose own membership becomes revoked cannot create later exports. Previously journaled immutable packs remain historical evidence and are still subject to receiver sequence and membership rules.

## Consequences

Enrolled Vault installations can converge on the same denial without copying private signing keys or trusting an unsigned renderer command. Delivery is still manual: a disconnected peer remains unaware until it imports the authority pack. Revocation therefore limits future accepted activity after delivery; it does not erase previously accepted events and it is not a remote wipe.

The unknown-device tombstone deliberately favors safety over convenience. Re-enrolling the same key after a propagated revocation requires an explicit future recovery design rather than ordinary registration.

Enrollment cancellation and expiry, folder or Git transport, and renderer controls remain separate lifecycle and delivery slices.

## Validation

- local revocation records the authority-bound portable event and rejects a revoked authority;
- a two-Vault enrollment round trip exports the source revocation, applies it on the target, and prevents the target identity from exporting again;
- an unknown target becomes a revoked tombstone and late registration is rejected;
- duplicate equivalent replay is idempotent while key, authority, revision, and signature mismatches fail closed;
- the complete Go suite passes.
