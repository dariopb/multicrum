---
name: multicrum-control
description: Create, coordinate, monitor, and clean up multiple terminal agents or tools through a named multicrum server. Use when a task should be delegated to parallel sub-agents, when different CLI agents need separate persistent PTY sessions, or when an agent must send prompts and consume output without hiding sessions from human TUI or Web UI viewers.
compatibility: Requires a running multicrum owner with multicrum-control protocol v1 and either the multicrum control CLI or a compatible client SDK.
metadata:
  author: multicrum
  version: "1.0"
---

# Multicrum Control

Use multicrum as a visible, persistent terminal-session supervisor for
sub-agents and tools. Every connection and session created through this skill
must appear in the ordinary multicrum server tree so a human can attach with:

```text
multicrum --server <server-name>
```

Do not create hidden subprocesses as a substitute for multicrum sessions when
this skill is active.

## Core rules

1. Use stable `connectionId`, `sessionId`, and `generation` values returned by
   the control protocol. Never retain mutable UI indexes as identity.
2. Give connections and sessions meaningful human-readable names.
3. Subscribe to output and lifecycle events before sending the first prompt.
4. Send multiline prompts with `session.paste`; send Enter separately or use
   `session.sendText` with `submit: true`.
5. Treat PTY output as an arbitrary byte stream. ANSI escapes, UTF-8 characters,
   and terminal replies may be split across chunks.
6. Prefer native `agent.state` events when available. Silence alone does not
   prove that an agent completed its turn.
7. Do not steal shared TUI focus unless explicitly requested.
8. Default created sessions to `retain` so humans can inspect or resume them.
9. Never remove, interrupt, or resize sessions outside the capability scope.
10. Never encode control messages into a child session's stdout.

See [references/control-methods.md](references/control-methods.md) for the
method and event quick reference.

## Discover the control context

When running inside a multicrum-managed parent session, first read:

```text
MULTICRUM_CONTROL_ENDPOINT
MULTICRUM_CONTROL_TOKEN
MULTICRUM_PARENT_SESSION_ID
MULTICRUM_SERVER
```

Use the injected short-lived token. Do not print it, pass it in process
arguments, save it in repository files, or include it in prompts.

Invoke control methods through the installed multicrum executable:

```text
multicrum call --method METHOD --params 'JSON'
```

The executable reads the injected endpoint, token, and server environment
automatically.

When running outside a managed session, use the server name and credential
provided by the user or host application. Do not guess a token or connect to an
unrelated server.

## Standard orchestration workflow

### 1. Inspect limits and existing objects

Call:

```text
server.get
connection.list
session.list
```

Check session quotas, allowed commands, cwd roots, SSH permission, and visible
objects before creating anything.

Reuse the subject session's current connection when the controlling
application explicitly supplies its `connectionId`. Do not create another
connection in that case. Otherwise, reuse a connection only when its labels
and purpose match the current task or create a dedicated connection:

```json
{
  "method": "connection.create",
  "params": {
    "name": "feature-42",
    "labels": {
      "task": "feature-42"
    },
    "idempotencyKey": "feature-42-connection"
  }
}
```

### 2. Define non-overlapping roles

Create separate sessions for independent responsibilities, for example:

| Session title | Command | Responsibility |
|---|---|---|
| `implementer` | interactive shell, then `copilot --no-mouse` | Implement the requested change |
| `reviewer` | `crush` | Review the resulting diff |
| `tests` | `bash` | Run tests and builds |

Do not ask multiple agents to edit the same files concurrently unless the
workflow explicitly handles conflicts.

### 3. Create sessions

Use argv arrays, not implicit shell strings:

```json
{
  "method": "session.create",
  "params": {
    "connectionId": "con_...",
    "title": "implementer",
    "backend": {
      "kind": "local"
    },
    "cmd": [
      "bash"
    ],
    "cwd": "/absolute/path/to/repository",
    "terminal": {
      "cols": 120,
      "rows": 36
    },
    "ownership": {
      "cleanup": "retain",
      "parentSessionId": "ses_parent"
    },
    "labels": {
      "role": "implementation",
      "task": "feature-42"
    },
    "idempotencyKey": "feature-42-implementer"
  }
}
```

For an interactive agent, create the ordinary shell-backed session first, then
start the agent through terminal input exactly as a user would:

```json
{
  "method": "session.sendText",
  "params": {
    "sessionId": "ses_...",
    "generation": 1,
    "text": "copilot --no-mouse",
    "submit": true
  }
}
```

Do not make `copilot` the session's root command. Starting the interactive shell
first establishes the same job-control and terminal environment as a
user-created session.

If shell syntax is necessary, invoke the shell explicitly:

```json
{
  "cmd": [
    "bash",
    "-lc",
    "make test"
  ]
}
```

Record each returned `sessionId` and `generation`.

### 4. Subscribe before sending input

Subscribe to:

```text
session.output
session.lifecycle
session.metadata
agent.state
```

Subscribe by stable session ID. Track each session's output sequence
independently; there is no total ordering across sessions.

If the protocol reports `session.outputGap`, request `session.snapshot`, resume
from the available sequence, and clearly mark that the raw transcript has a
gap.

### 5. Send prompts safely

For multiline instructions:

```json
{
  "method": "session.paste",
  "params": {
    "sessionId": "ses_...",
    "generation": 1,
    "text": "Implement the API change.\nRun the focused tests.\nDo not edit unrelated files."
  }
}
```

Then submit:

```json
{
  "method": "session.sendText",
  "params": {
    "sessionId": "ses_...",
    "generation": 1,
    "text": "",
    "submit": true
  }
}
```

`session.paste` preserves bracketed-paste semantics when the child enabled DEC
2004. Do not manually add `ESC[200~` or `ESC[201~`.

### 6. Monitor each child independently

Maintain per-session state:

```text
session ID
generation
last contiguous output sequence
process lifecycle
agent provider/state/source/confidence
deadline
assigned role
```

Use `session.await` with bounded timeouts. Preferred completion condition:

```json
{
  "condition": {
    "any": [
      {
        "agentState": [
          "idle",
          "done",
          "blocked"
        ]
      },
      {
        "exit": true
      }
    ]
  },
  "timeoutMs": 120000
}
```

If native state is unavailable, combine a screen/output pattern with a
conservative quiet period. Report the result as heuristic, not guaranteed
completion.

When an agent becomes `blocked`, surface the permission or question to the
human. Do not blindly approve destructive or security-sensitive actions.

### 7. Coordinate results

Use external durable state such as repository changes, files, commits, test
results, or an explicit artifact path to hand work between agents. Do not rely
only on copied terminal prose.

Before starting a reviewer:

1. Confirm the implementer reached a trustworthy terminal state.
2. Confirm the expected repository changes exist.
3. Tell the reviewer which branch, diff, files, or commit to inspect.

### 8. Preserve human visibility

Automation-created objects must remain visible in the normal TUI and browser.
After create, rename, move, respawn, or close, expect metadata events and
ordinary UI updates.

Do not focus a session merely to send it input. A human may be inspecting
another session.

When the user explicitly asks to view, show, or activate a session, call:

```text
multicrum call --method session.focus --params '{"sessionId":"ses_..."}'
```

If the user asks to inspect progress, provide the server and connection names:

```text
multicrum --server orchestrator
connection: feature-42
```

### 9. Clean up deliberately

Use:

- `retain` when humans may inspect, resume, or debug the sessions;
- `terminate` for disposable tools after their output has been consumed;
- `remove` only when the session is no longer needed and removal is authorized.

The wire method is always `session.close`. Put the action in its `mode` field:

```json
{
  "method": "session.close",
  "params": {
    "sessionId": "ses_...",
    "generation": 1,
    "mode": "remove"
  }
}
```

There are no canonical `session.remove` or `session.terminate` methods.

Never terminate another controller's retained session. On timeout, prefer an
interrupt before forced termination when safe.

## Failure handling

### Session exits early

Read the final output, exit metadata, and current snapshot. Respawn only if the
task is retryable and use the new generation returned by the owner.

### Controller reconnects

Reconnect with the controller identity when supported. List sessions by labels
and creator, then resume subscriptions from the last acknowledged sequence.
Use idempotency keys before retrying create requests.

### Output is not parseable

Do not assume chunks are lines. Feed bytes through a terminal-aware decoder or
request `plain-screen`/`ansi-screen` snapshots.

### Agent appears idle but work is incomplete

Inspect repository or artifact state and send a follow-up prompt. Agent state
describes terminal lifecycle, not task correctness.

### Human modifies a child session

Accept human actions as authoritative. Rename, move, resize, respawn, and close
events may occur at any time. Re-resolve metadata by stable ID before the next
mutation.

## Completion report

When reporting orchestration results, include:

- named multicrum server;
- connection name;
- session titles and final states;
- durable artifacts produced;
- blocked or failed sessions;
- whether retained sessions remain available for human inspection.

Do not include control tokens or raw escape sequences.
