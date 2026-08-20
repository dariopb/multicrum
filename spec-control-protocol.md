# Multicrum Automation Control Protocol

## Summary

Multicrum should expose an automation protocol that lets a trusted application
or agent create, observe, and control multiple child sessions without driving
the TUI or browser UI.

The protocol must preserve multicrum's central architectural rule: child agents
are terminal applications running behind real PTYs. The control protocol
manages PTYs and session lifecycle; it does not replace terminal input/output
with an agent-specific chat protocol.

A controller should be able to:

1. Create connections/workspaces.
2. Create sessions with different commands, working directories, environments,
   and local or SSH backends.
3. Send text, paste events, key sequences, or raw bytes to one session.
4. Subscribe to raw PTY output and lifecycle events for many sessions at once.
5. Read a current terminal snapshot and bounded replay history.
6. Observe optional agent state such as `working`, `blocked`, `done`, or `idle`.
7. Wait for explicit conditions without assuming that silence always means a
   completed agent turn.
8. Close, respawn, move, rename, or retain sessions.
9. Delegate a restricted capability to a child agent so it can create and
   control its own descendants.

The recommended protocol name is:

```text
multicrum-control
```

The initial protocol version is `1`.

## Non-goals

Version 1 does not:

- define a universal structured prompt/response format for Copilot, Crush,
  Claude Code, Codex, or other terminal agents;
- infer that a response is complete solely because no bytes arrived recently;
- scrape the owner-rendered TUI;
- encode control messages into a child PTY's stdout;
- expose the local TUI attach protocol as an automation API;
- promise lossless, unbounded transcript storage;
- allow an untrusted child process to control every session owned by the user.

Provider integrations may add higher-confidence lifecycle events, but raw PTY
operation must remain available for every terminal application.

## Architecture

The owner daemon hosts a control endpoint alongside the existing TUI attach and
optional WebSocket endpoints.

```text
controller / parent agent
        |
        | multicrum-control
        v
multicrum owner daemon
  |
  +-- connection A
  |     +-- session: copilot
  |     +-- session: crush
  |
  +-- connection B
        +-- session: shell
        +-- session: test runner
```

Automation-created connections and sessions are not a separate hidden pool.
They are inserted into the owner's ordinary:

```text
server -> connections -> sessions
```

tree and use the same `connectionState`, `SessionManager`, PTY, metadata,
rendering, persistence, and lifecycle paths as objects created from the TUI or
browser.

If an application creates objects under server `orchestrator`, a human can
attach normally:

```text
multicrum --server orchestrator
```

The attached TUI immediately shows the automation-created connections and
sessions. The human can focus a connection by name through the connections
dialog, switch sessions, inspect output, send input, rename, resize, move, or
close them subject to the same safeguards as any human-created session.

Likewise, if the owner was started with WebSocket support, the browser UI shows
the same objects through its existing metadata broadcasts. No special
automation viewer is required.

The control endpoint should be separate from the owner-rendered TUI stream.
They have different trust, framing, backpressure, and lifecycle requirements.
An implementation may share listener infrastructure internally, but automation
clients must use a distinct handshake and protocol namespace.

Recommended endpoints:

```text
Unix:    /tmp/multicrum-$UID/<server>.control.sock
Windows: per-user named pipe, or a loopback address recorded beside the
         existing server address file
```

The Unix socket directory and socket must only be accessible by the owning
user. Remote network exposure is out of scope for version 1.

The existing named-server attach endpoint remains authoritative for human
access. Knowing the server name and having the normal same-user operating-system
permissions is sufficient to attach exactly as it is for a manually created
server. The automation capability token is only required for control-protocol
operations; it must not be required merely to view or interact through the
normal TUI attach path.

## Identity Model

Automation must never use mutable zero-based UI indexes as durable identity.
Indexes change when sessions are moved or removed.

Every object has an opaque stable ID:

| Object | Example | Lifetime |
|---|---|---|
| Server | `srv_01J...` | One owner process |
| Connection | `con_01J...` | Until removed |
| Session | `ses_01J...` | Until removed |
| Session generation | `4` | Incremented on each process start/respawn |
| Controller | `ctl_01J...` | One authenticated connection or resumable identity |
| Subscription | `sub_01J...` | Until cancelled/disconnected |

The existing session runtime ID can seed the first implementation, but IDs are
wire-level opaque strings. Clients must not parse PID, index, or generation
from an ID.

All session-scoped events include both `sessionId` and `generation`. Delayed
output or state events from a previous generation must never be attributed to
the replacement process.

## Transport and Framing

Reuse the existing length-delimited frame shape:

```text
uint32 big-endian frame length, including the type byte
byte frame type
payload bytes
```

The control protocol should have its own frame type range:

| Type | Name | Direction | Payload |
|---:|---|---|---|
| `0x20` | `ControlHello` | client -> owner | JSON |
| `0x21` | `ControlWelcome` | owner -> client | JSON |
| `0x22` | `ControlRequest` | client -> owner | JSON |
| `0x23` | `ControlResponse` | owner -> client | JSON |
| `0x24` | `ControlEvent` | owner -> client | JSON |
| `0x25` | `ControlOutput` | owner -> client | binary header plus raw PTY bytes |
| `0x26` | `ControlInput` | client -> owner | binary header plus raw input bytes |
| `0x27` | `ControlAck` | either direction | JSON |

JSON is UTF-8. Unknown JSON fields must be ignored. Unknown frame types are a
protocol error unless a negotiated capability says otherwise.

The existing one-MiB frame limit is sufficient for control messages. Raw PTY
chunks should normally remain below 64 KiB.

## Handshake

The client starts with:

```json
{
  "protocol": "multicrum-control",
  "version": 1,
  "server": "default",
  "clientName": "planner-agent",
  "clientVersion": "0.4.0",
  "token": "capability-token",
  "resumeControllerId": "ctl_01J...",
  "capabilities": [
    "binary-output",
    "output-replay",
    "agent-state"
  ]
}
```

The owner replies:

```json
{
  "protocol": "multicrum-control",
  "version": 1,
  "serverId": "srv_01J...",
  "serverName": "default",
  "controllerId": "ctl_01J...",
  "ownerPid": 43120,
  "capabilities": [
    "binary-input",
    "binary-output",
    "output-replay",
    "agent-state",
    "session-await",
    "delegation"
  ],
  "limits": {
    "maxFrameBytes": 1048576,
    "maxSessions": 32,
    "replayBytesPerSession": 262144
  }
}
```

Version negotiation is exact in version 1. A client requesting an unsupported
version receives an error and the connection closes.

## Request and Response Envelope

Requests use client-generated IDs:

```json
{
  "id": "req-17",
  "method": "session.create",
  "params": {}
}
```

Successful response:

```json
{
  "id": "req-17",
  "ok": true,
  "result": {}
}
```

Failed response:

```json
{
  "id": "req-17",
  "ok": false,
  "error": {
    "code": "invalid_argument",
    "message": "cmd must contain at least one argument",
    "details": {
      "field": "cmd"
    }
  }
}
```

Request IDs are unique among outstanding requests on one controller
connection. Responses may arrive out of order.

Mutating methods accept an optional `idempotencyKey`. The owner caches the
result for a bounded period so reconnecting clients do not accidentally create
duplicate sessions.

## Error Codes

Version 1 defines:

| Code | Meaning |
|---|---|
| `invalid_argument` | Invalid or missing request data |
| `not_found` | Server, connection, session, generation, or subscription does not exist |
| `already_exists` | Name or idempotency key conflicts with an existing object |
| `permission_denied` | Capability does not allow the operation |
| `resource_exhausted` | Session, queue, replay, or frame limit exceeded |
| `failed_precondition` | Object exists but is in an incompatible state |
| `unavailable` | Backend or owner service is temporarily unavailable |
| `timeout` | Requested wait deadline expired |
| `output_gap` | Requested output is older than retained replay |
| `internal` | Unexpected owner failure |

Errors must not include credentials, environment secrets, prompt contents, or
unredacted command arguments unless the requester is authorized to read them.

## Core Object Methods

### `server.get`

Returns owner identity, startup settings, capabilities, and limits.

### `connection.list`

Returns ordered connection metadata:

```json
{
  "connections": [
    {
      "connectionId": "con_01J...",
      "name": "implementation",
      "sessionCount": 3,
      "createdBy": "ctl_01J..."
    }
  ]
}
```

### `connection.create`

```json
{
  "name": "reviewers",
  "labels": {
    "task": "review-pr-42"
  },
  "idempotencyKey": "review-pr-42-connection"
}
```

The connection becomes visible immediately in all attached TUI and Web UI
clients. Connection names should be unique within a server for unambiguous
human navigation, while `connectionId` remains the durable protocol identity.

### `connection.rename`

```json
{
  "connectionId": "con_01J...",
  "name": "security-review"
}
```

### `connection.remove`

```json
{
  "connectionId": "con_01J...",
  "sessionPolicy": "terminate"
}
```

`sessionPolicy` is `reject`, `terminate`, or `move`. `move` also requires a
target connection ID.

## Session Creation

### `session.create`

Local example:

```json
{
  "connectionId": "con_01J...",
  "title": "copilot-api",
  "backend": {
    "kind": "local"
  },
  "cmd": [
    "copilot",
    "--model",
    "gpt-5"
  ],
  "cwd": "/home/user/project",
  "env": {
    "ROLE": "api-implementer"
  },
  "terminal": {
    "cols": 120,
    "rows": 36,
    "term": "xterm-256color"
  },
  "ownership": {
    "cleanup": "retain",
    "parentSessionId": "ses_parent"
  },
  "labels": {
    "role": "implementation",
    "task": "api"
  },
  "idempotencyKey": "task-api-agent"
}
```

SSH example:

```json
{
  "connectionId": "con_01J...",
  "title": "remote-tests",
  "backend": {
    "kind": "ssh",
    "target": "builder@example.com",
    "port": "22",
    "keyRef": "default",
    "verifyHostKey": true
  },
  "cmd": [
    "bash",
    "-lc",
    "cd /workspace && copilot"
  ],
  "terminal": {
    "cols": 140,
    "rows": 42
  }
}
```

Successful result:

```json
{
  "session": {
    "sessionId": "ses_01J...",
    "generation": 1,
    "connectionId": "con_01J...",
    "index": 2,
    "title": "copilot-api",
    "state": "running",
    "pid": 44102,
    "backend": "local",
    "createdBy": "ctl_01J..."
  }
}
```

`index` is informational and may change. Controllers use `sessionId`.

The new session appears immediately as an ordinary tab inside the target
connection. Output produced before a human attaches remains available through
the normal bounded scrollback and replay mechanisms.

Commands are argv arrays and are not implicitly shell-expanded. A controller
that needs shell syntax explicitly starts a shell, for example:

```json
{
  "cmd": ["bash", "-lc", "make test && copilot"]
}
```

Environment values are additions or overrides to the owner's sanitized child
environment. Dangerous owner-only variables and multicrum capability tokens
cannot be overwritten.

### `session.list`

Filters may include connection, labels, creator, parent session, process state,
or detected agent provider/state.

### `session.get`

Returns current metadata, command visibility subject to authorization, process
state, terminal dimensions, output sequence, and optional agent state.

### `session.rename`

Changes display metadata only; it does not send terminal input.

### `session.focus`

Makes the session and its containing connection active in the shared TUI and
browser metadata. It requires `sessionId`; `generation` is optional because
focus does not mutate the child process. Automation must not focus sessions
unless the user explicitly requests the visibility change.

### `session.move`

Moves a session to another connection or ordered position without changing its
stable ID.

### `session.resize`

```json
{
  "sessionId": "ses_01J...",
  "generation": 1,
  "cols": 100,
  "rows": 30
}
```

The existing last-resizer-wins rule applies. Automation clients should resize
only sessions they actively control.

### `session.respawn`

Starts a new process using the stored session configuration and increments
`generation`. Output sequence numbers restart at zero for the new generation.

### `session.close`

```json
{
  "sessionId": "ses_01J...",
  "mode": "terminate",
  "graceMs": 3000
}
```

Modes:

- `input-eof`: close the input side when supported;
- `interrupt`: send the platform-appropriate interrupt;
- `terminate`: request graceful process termination, then force after `graceMs`;
- `remove`: terminate if needed and remove session metadata.

The protocol must not silently kill the last interactive session merely
because the TUI currently protects it. Automation policy is explicit and
capability-controlled.

## Sending Input

There are three input levels.

### `session.sendText`

For ordinary Unicode input:

```json
{
  "sessionId": "ses_01J...",
  "generation": 1,
  "text": "Investigate the failing authentication tests.",
  "submit": true
}
```

`submit: true` appends carriage return after the text. It is a convenience, not
an agent-turn guarantee.

### `session.paste`

```json
{
  "sessionId": "ses_01J...",
  "generation": 1,
  "text": "line one\nline two\nline three"
}
```

The owner checks the session's tracked DEC 2004 state. If bracketed paste is
enabled, it writes:

```text
ESC [ 200 ~
payload
ESC [ 201 ~
```

Otherwise it writes the payload unchanged. This mirrors local TUI paste
behavior and prevents embedded newlines from becoming accidental submissions
when the child supports bracketed paste.

### `ControlInput` binary frame

For exact terminal bytes. The payload is:

```text
uint16 big-endian JSON header length
JSON header
raw bytes
```

Header:

```json
{
  "requestId": "req-22",
  "sessionId": "ses_01J...",
  "generation": 1
}
```

Binary input is required for key protocols, escape sequences, arbitrary bytes,
and applications that already implement terminal encoding.

The owner replies with a normal `ControlResponse` containing the accepted byte
count. A successful response means the bytes were written to the PTY endpoint,
not that the child processed them.

## Output and Event Subscriptions

### `subscribe`

```json
{
  "streams": [
    "session.output",
    "session.lifecycle",
    "session.metadata",
    "agent.state"
  ],
  "sessionIds": [
    "ses_01J...",
    "ses_01K..."
  ],
  "from": {
    "ses_01J...": {
      "generation": 1,
      "sequence": 184
    }
  }
}
```

Result:

```json
{
  "subscriptionId": "sub_01J..."
}
```

An empty `sessionIds` list means all sessions visible to the capability,
including later-created sessions.

### Raw output

Each generation has a monotonically increasing byte sequence. `ControlOutput`
uses:

```text
uint16 big-endian JSON header length
JSON header
raw PTY bytes
```

Header:

```json
{
  "subscriptionId": "sub_01J...",
  "sessionId": "ses_01J...",
  "generation": 1,
  "sequence": 184,
  "length": 57,
  "timestamp": "2026-08-18T20:58:18.307-07:00"
}
```

`sequence` is the offset of the first payload byte. The next contiguous event
starts at `sequence + length`.

Output is exactly what the child wrote to the PTY. It may contain partial UTF-8
characters, ANSI escapes split across frames, carriage returns, cursor
movement, progress redraws, and terminal queries. Controllers must treat it as
a byte stream.

### Replay and gaps

The owner retains a bounded raw replay buffer per session generation. The
initial implementation can reuse the existing 256-KiB raw history cap.

If the requested sequence is available, replay begins there. Otherwise the
owner sends:

```json
{
  "event": "session.outputGap",
  "sessionId": "ses_01J...",
  "generation": 1,
  "requestedSequence": 10,
  "availableSequence": 9214
}
```

The client may then request a terminal snapshot and continue from the current
sequence.

### Terminal snapshot

`session.snapshot` supports:

```json
{
  "sessionId": "ses_01J...",
  "generation": 1,
  "format": "raw-replay"
}
```

Formats:

| Format | Meaning |
|---|---|
| `raw-replay` | Existing bounded replay bytes suitable for reconstructing xterm state |
| `ansi-screen` | Current rows-by-columns screen rendered with ANSI SGR |
| `plain-screen` | Current visible screen as plain rows |
| `plain-buffer` | Current semantic scrollback plus visible rows, bounded by server policy |

Result includes terminal dimensions, cursor metadata, current output sequence,
and whether the snapshot is complete or bounded.

`raw-replay` is not a complete transcript and must not be presented as one.

## Lifecycle Events

Events use the common envelope:

```json
{
  "event": "session.started",
  "eventId": "evt_01J...",
  "timestamp": "2026-08-18T21:00:00Z",
  "sessionId": "ses_01J...",
  "generation": 1,
  "data": {}
}
```

Required lifecycle events:

| Event | Meaning |
|---|---|
| `session.created` | Session metadata allocated |
| `session.started` | PTY process started |
| `session.exited` | Process exited or PTY closed |
| `session.respawned` | New generation started |
| `session.renamed` | Display title changed |
| `session.moved` | Connection or position changed |
| `session.resized` | Authoritative PTY size changed |
| `session.removed` | Session metadata removed |
| `connection.created` | Connection created |
| `connection.renamed` | Connection renamed |
| `connection.removed` | Connection removed |

`session.exited` includes an exit code or signal when available. SSH disconnects
may report a transport reason instead.

## Agent State

If multicrum detects a supported agent, subscribers receive:

```json
{
  "event": "agent.state",
  "sessionId": "ses_01J...",
  "generation": 1,
  "data": {
    "provider": "copilot",
    "state": "working",
    "source": "native",
    "confidence": "high",
    "sequence": 14
  }
}
```

States reuse the existing model:

```text
unknown
working
blocked
done
idle
```

Controllers must inspect `source` and `confidence`. Screen heuristics are less
authoritative than native lifecycle hooks.

The automation protocol must not convert `done` or `idle` into a universal
response boundary. Some agents redraw asynchronously, run background work, or
return to an input editor before all external processes finish.

## Waiting for Conditions

### `session.await`

`session.await` provides bounded synchronization primitives:

```json
{
  "sessionId": "ses_01J...",
  "generation": 1,
  "timeoutMs": 120000,
  "condition": {
    "any": [
      {
        "agentState": ["idle", "done", "blocked"]
      },
      {
        "exit": true
      }
    ]
  }
}
```

Supported conditions:

- `agentState`: optional detected states;
- `exit`: process exit;
- `outputRegex`: regex over a bounded decoded output window;
- `screenRegex`: regex over the current plain screen;
- `quietMs`: no PTY output for a duration;
- `sequenceAtLeast`: output stream reached an offset;
- `all` / `any`: condition composition.

`quietMs` alone is not a reliable agent-completion signal and should produce a
result reason of `quiet`, not `completed`.

Result:

```json
{
  "matched": true,
  "reason": "agentState",
  "agentState": "idle",
  "outputSequence": 5512
}
```

Waits are cancelled if their controller disconnects unless `detached: true`
was explicitly allowed by its capability.

## Suggested Parent-Agent Workflow

1. Connect and authenticate.
2. Create a dedicated connection for the task.
3. Create child sessions with distinct commands and role labels.
4. Subscribe to output, lifecycle, and agent-state streams before sending
   prompts.
5. Send each prompt with `session.paste` followed by an explicit Enter, or use
   `session.sendText` with `submit: true`.
6. Consume output independently by stable session ID.
7. Use native agent state when available; otherwise combine output patterns,
   screen snapshots, and conservative timeouts.
8. Interrupt or close a child that exceeds its deadline.
9. Retain sessions for human inspection or remove them according to policy.

Example:

```json
{"id":"1","method":"connection.create","params":{"name":"feature-42"}}
{"id":"2","method":"session.create","params":{"connectionId":"con_feature","title":"implementer","cmd":["copilot"],"labels":{"role":"implement"}}}
{"id":"3","method":"session.create","params":{"connectionId":"con_feature","title":"reviewer","cmd":["crush"],"labels":{"role":"review"}}}
{"id":"4","method":"subscribe","params":{"streams":["session.output","session.lifecycle","agent.state"],"sessionIds":["ses_impl","ses_review"]}}
{"id":"5","method":"session.paste","params":{"sessionId":"ses_impl","generation":1,"text":"Implement the requested API change."}}
{"id":"6","method":"session.sendText","params":{"sessionId":"ses_impl","generation":1,"text":"","submit":true}}
```

The reviewer prompt can be sent after the implementer reaches a trusted state
or after an external repository event indicates that reviewable changes exist.

## Ownership and Cleanup

Every created object records its creator controller and optional parent
session. Session cleanup policy is:

| Policy | On controller disconnect |
|---|---|
| `retain` | Keep running and allow later reattachment |
| `terminate` | Gracefully stop, then force after timeout |
| `remove` | Stop and remove metadata |
| `inherit` | Follow the parent session or connection policy |

The default should be `retain`, matching multicrum's persistent-session model.
Short-lived orchestration tools may explicitly choose `terminate`.

Controller disconnect must never implicitly stop sessions owned by another
controller.

`retain` also means the complete server tree remains available to later human
attach clients after the creating application exits. An orchestration process
must not be the lifetime owner of the multicrum daemon or its PTYs.

## Delegation to Child Agents

A parent controller can request a restricted child capability:

### `capability.delegate`

```json
{
  "subjectSessionId": "ses_01J...",
  "expiresInMs": 3600000,
  "permissions": [
    "connection.create",
    "session.create",
    "session.get",
    "session.sendText",
    "session.paste",
    "session.subscribe",
    "session.await",
    "session.close"
  ],
  "scope": {
    "createdBySubject": true,
    "maxConnections": 2,
    "maxSessions": 6,
    "allowedCommands": [
      "copilot",
      "crush",
      "bash"
    ],
    "allowedCwdRoots": [
      "/home/user/project"
    ],
    "allowSSH": false
  }
}
```

Result:

```json
{
  "endpoint": "/tmp/multicrum-1000/default.control.sock",
  "token": "short-lived-capability-token",
  "expiresAt": "2026-08-18T22:00:00Z"
}
```

The owner may inject these into the subject session:

```text
MULTICRUM_CONTROL_ENDPOINT
MULTICRUM_CONTROL_TOKEN
MULTICRUM_PARENT_SESSION_ID
```

Tokens are bearer credentials. They must not appear in TUI metadata, logs,
process arguments, saved layout files, or child output. Environment inheritance
means descendants can receive the token, so scopes and expiration are
mandatory.

A delegated controller can only see and mutate objects allowed by its scope.
`createdBySubject: true` means it may manage objects created with that
capability, not pre-existing user sessions.

Capability revocation immediately rejects new requests. Existing child
sessions follow their cleanup policy.

## Security

The control endpoint can execute arbitrary commands and inject terminal input.
It is equivalent to local code execution as the multicrum owner.

Required controls:

1. Per-user endpoint permissions.
2. Random owner token for full-control clients.
3. Short-lived scoped tokens for child agents.
4. Constant-time token comparison.
5. Command, cwd-root, SSH, environment, and session-count restrictions in
   delegated capabilities.
6. No secrets in logs or error messages.
7. Explicit authorization for transcript/snapshot access.
8. Bounded request sizes, output queues, subscriptions, and waits.
9. Audit events for create, input, close, delegation, and revocation without
   logging prompt or output contents by default.
10. Generation checks on every mutating session request.

The owner must reject path traversal outside delegated cwd roots after resolving
symlinks according to documented policy.

Shell command strings should not be accepted as an implicit convenience in the
core protocol. Use argv arrays to avoid accidental shell injection.

## Backpressure and Reliability

Each controller has bounded event and output queues.

- Lifecycle and control responses take priority over PTY output.
- A slow controller must not block PTY read loops, the Bubble Tea update loop,
  browser clients, or other controllers.
- When output cannot be queued, the owner records the first dropped sequence
  and sends `session.outputGap` when the client catches up.
- The owner may disconnect a client that cannot consume control responses.
- Clients acknowledge the highest contiguous output sequence per session with
  `ControlAck`.
- Acknowledgement allows the owner to discard per-client replay bookkeeping;
  it does not expand the global replay cap.

Control responses are at-most-once per live connection. Idempotency keys provide
safe retry for selected mutating requests after reconnect.

## Ordering

For one session generation:

1. `session.started` precedes output.
2. Output sequence order is total and byte-contiguous unless a gap is reported.
3. `session.exited` follows the final output accepted by the owner.
4. Agent-state events carry their own source sequence and may be concurrent with
   PTY output.

There is no total ordering across different sessions. Controllers must not use
arrival order to infer causality between sub-agents.

## Interaction with Human Viewers

Automation and humans may observe the same session.

- Control-created objects are rendered and controlled through the exact same
  TUI and Web UI paths as human-created objects.
- A human attaches by the existing server name, for example
  `multicrum --server orchestrator`; there is no separate automation attach
  command.
- Connection names and session titles are human-facing metadata and should be
  chosen meaningfully by the controller.
- Metadata broadcasts occur after every automated create, remove, rename, move,
  respawn, or state change so already-attached clients update immediately.
- Input from different controllers is serialized at the PTY write boundary,
  but the protocol cannot make concurrent editing semantically safe.
- The last-resizer-wins terminal sizing rule remains in effect.
- Focus is UI metadata and is not required for automation input or output.
- Creating or controlling a session does not have to steal focus from a human.
  A request may explicitly ask to focus the object, but the default is to leave
  the shared UI focus unchanged.
- Human rename, move, resize, respawn, or close actions produce normal events.

An optional future input lease may let one controller claim exclusive input for
a bounded period. Version 1 should expose ownership metadata but not require
leases for all sessions.

## Compatibility with Existing Multicrum APIs

The first implementation maps naturally onto existing packages:

| Protocol operation | Existing primitive |
|---|---|
| Create local session | `SessionManager.New` / `NewInDir` |
| Create SSH session | `SessionManager.NewWithSSH` |
| Send bytes | `Session.Write` |
| Resize | `SessionManager.ResizeOne` |
| Raw output | `SessionManager.SendOutput` / `OutputMsg.Data` |
| Exit event | `SessionManager.SendExit` |
| Screen snapshot | `Session.Screen().RenderSnapshot()` |
| Raw replay | `Session.Screen().RawSnapshot()` |
| Plain buffer | `Session.Screen().BufferLines()` |
| Agent state | existing `agentdetect` status model |
| Stable identity | session runtime ID, promoted to public opaque ID |

The manager currently reports output by mutable index. The control layer should
bind callbacks to the session object or resolve the stable ID while holding the
manager's synchronization, then emit wire events by `sessionId`.

Connections also need stable IDs instead of relying on names or slice indexes.

## Implementation Phases

### Phase 1: Embedded control service

- Add stable server, connection, and session IDs.
- Add a separate per-user control endpoint.
- Implement handshake, request/response, list/create/get/send/resize/close.
- Implement output and lifecycle subscriptions.
- Add bounded replay and output-gap events.
- Route all automated mutations through the normal owner state and metadata
  broadcast paths so objects are immediately visible to existing and future
  TUI/Web UI clients.
- Add an end-to-end test that creates a connection and sessions through the
  control endpoint, attaches through `multicrum --server NAME`, and confirms
  that the same IDs, titles, output, and lifecycle changes are visible.

### Phase 2: State and synchronization

- Expose agent-state events.
- Implement snapshots and `session.await`.
- Add idempotency keys and reconnect replay.
- Preserve events across session reindexing and respawn generations.

### Phase 3: Delegated sub-agent control

- Implement scoped capabilities, expiration, and revocation.
- Inject restricted endpoint/token variables into approved sessions.
- Add quotas, command allowlists, cwd restrictions, and audit events.

### Phase 4: Client SDK and CLI

Provide a Go client first:

```go
client, err := control.Dial(control.Options{
    Server: "default",
    Token:  token,
})

session, err := client.CreateSession(ctx, control.CreateSessionRequest{
    ConnectionID: connection.ID,
    Command:      []string{"copilot"},
    Cwd:          repo,
})

outputs, err := client.SubscribeOutput(ctx, session.ID, session.Generation, 0)
err = client.Paste(ctx, session.ID, session.Generation, prompt)
err = client.SendText(ctx, session.ID, session.Generation, "", true)
```

A diagnostic CLI can wrap the same SDK:

```text
multicrum control connections
multicrum control create --connection feature --title reviewer -- crush
multicrum control send --session ses_... --paste-file prompt.txt --enter
multicrum control output --session ses_... --follow
multicrum control await --session ses_... --agent-state idle,done,blocked
multicrum control close --session ses_...
```

The CLI must not make the wire protocol depend on terminal formatting.

### Agent skill distribution

Ship an Agent Skills-compatible package at:

```text
skills/multicrum-control/SKILL.md
```

The skill teaches a controlling agent how to discover its scoped endpoint and
token, create visible named connections and sessions, subscribe before sending
prompts, preserve bracketed paste, correlate output by stable ID/generation,
wait conservatively, handle blocked agents, retain sessions for human
inspection, and clean up only within its capability scope.

The skill is an operational guide over this protocol, not an alternative
transport. Its examples and method reference must remain synchronized with the
implemented protocol and control CLI.

## Open Questions

1. Should full-control clients authenticate with a token file, peer credentials,
   or both?
2. Should Windows use named pipes immediately or reuse the current loopback
   address-file model?
3. Should retained event replay survive owner restart, or remain memory-only?
4. Should input leases be included in version 1?
5. Which command/cwd policies are appropriate defaults for delegated agents?
6. Should `session.await` regexes operate on raw output, ANSI-stripped output,
   semantic screen rows, or require the caller to choose explicitly?
7. Should a future provider adapter expose structured turn IDs while keeping
   the core PTY protocol provider-neutral?
