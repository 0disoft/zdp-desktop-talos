# ADR 0024: Owned Worker Client Session

- Status: Accepted
- Date: 2026-07-11

## Context

The worker server can enforce framed requests and process capabilities, but the desktop control plane needs a single owner for the worker process, stdio protocol, concurrent request correlation, cancellation, and shutdown. Exposing a generic message call would leak protocol strings into application code and make it possible to bypass the intended execution sequence.

## Decision

`internal/workeripc.Session` owns one worker process or an equivalent test transport.

- The public session surface is limited to handshake, start run, execute tool, cancel tool, shutdown, and close. It does not expose a generic message-type call.
- Exactly one reader goroutine owns stdout decoding. Writes are serialized, and responses are routed by generated request ID to bounded one-result channels.
- Unknown request IDs, wrong protocol versions, unexpected response types, malformed payloads, and framing failures terminate the session and fail all pending calls.
- Context cancellation releases the caller but retains the pending correlation until the late worker response is drained. A late response therefore cannot poison a healthy session as unsolicited traffic.
- Worker protocol error messages are mapped to a typed local error whose public error text includes only the stable code. Untrusted worker detail is not emitted by `Error()`.
- Production worker startup requires an existing absolute executable path, captures only bounded stderr, and supplies a new minimal environment rather than inheriting desktop credentials.
- Graceful shutdown waits for process reaping. Forced close terminates the worker and asynchronously reaps it.

## Consequences

The future execution coordinator can use a typed internal port without understanding frame syntax or worker process ownership. Worker exit is a session-wide failure rather than a silent per-call timeout.

The session remains an IPC adapter, not the authority for permission or attempt state. The coordinator must still evaluate the Permission Broker and commit the execution journal before dispatch.
