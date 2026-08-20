# Multicrum Control Method Reference

This is the concise method reference for the `multicrum-control` skill.

Invoke one request from a delegated session with:

```text
multicrum call --method METHOD --params 'JSON'
```

The client reads `MULTICRUM_CONTROL_ENDPOINT`, `MULTICRUM_CONTROL_TOKEN`, and
`MULTICRUM_SERVER` from its injected environment. Never include the token in
the command line.

## Discovery and metadata

| Method | Purpose |
|---|---|
| `server.get` | Read server identity, capabilities, and limits |
| `connection.list` | List visible connections |
| `session.list` | List visible sessions and filter by labels/creator/state |
| `session.get` | Read one session's current metadata |

## Connections

| Method | Purpose |
|---|---|
| `connection.create` | Create a visible workspace |
| `connection.rename` | Change its human-facing name |
| `connection.remove` | Remove it using an explicit session policy |

## Sessions

| Method | Purpose |
|---|---|
| `session.create` | Start a local or SSH PTY session |
| `session.focus` | Make a session and its containing connection active in shared UIs |
| `session.rename` | Change the tab title |
| `session.move` | Move/reorder without changing stable identity |
| `session.resize` | Apply last-resizer-wins PTY dimensions |
| `session.respawn` | Start a new process generation |
| `session.close` | Interrupt, terminate, or remove |
| `session.snapshot` | Read raw replay, ANSI screen, plain screen, or buffer |

## Input

| Method/frame | Purpose |
|---|---|
| `session.sendText` | Send Unicode text and optionally submit with Enter |
| `session.paste` | Send multiline text with tracked bracketed-paste behavior |
| `ControlInput` | Send exact raw terminal bytes |

Every input request should include `sessionId` and `generation`.

## Subscriptions

Call `subscribe` for one or more streams:

| Stream | Purpose |
|---|---|
| `session.output` | Sequenced raw PTY bytes |
| `session.lifecycle` | Start, exit, respawn, remove |
| `session.metadata` | Rename, move, resize |
| `agent.state` | Optional provider lifecycle state |

Track raw output with:

```text
sessionId + generation + sequence
```

On `session.outputGap`, request a snapshot and resume at the advertised
available sequence.

## Waiting

`session.await` supports:

```text
agentState
exit
outputRegex
screenRegex
quietMs
sequenceAtLeast
all
any
```

Always set a timeout. `quietMs` means quiet, not completed.

## Agent states

```text
unknown
working
blocked
done
idle
```

Prefer `source: native` over structured-terminal or screen-heuristic state.

## Stable identity

Use opaque IDs:

```text
serverId
connectionId
sessionId
controllerId
subscriptionId
```

UI indexes are informational and can change after move/remove.

Session `generation` changes after respawn. Reject stale events and requests
from earlier generations.

## Visibility

Everything created by this protocol is an ordinary multicrum object. Humans
attach with:

```text
multicrum --server <server-name>
```

The same connection names, session titles, output, and lifecycle state appear
in TUI and Web UI.

## Delegation

`capability.delegate` creates a short-lived token restricted by:

```text
allowed methods
objects created by the subject
maximum connections/sessions
allowed commands
allowed cwd roots
SSH permission
expiration
```

Never print or persist the delegated token.
