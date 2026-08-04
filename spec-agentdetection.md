# AI Agent Detection and State Reporting

## Summary

Multicrum should detect AI coding agents running inside its terminal sessions and
surface their state without coupling the PTY transport to an agent-specific
protocol.

The feature has two separate responsibilities:

1. **Presence detection** answers whether a supported agent is running in a
   session and identifies the provider.
2. **State detection** answers what that agent is currently doing.

Keeping these separate is important. A process tree can identify `copilot`, but
process state, CPU usage, or the presence of child tools cannot reliably
distinguish thinking, waiting for approval, and waiting for the next prompt.

Primary provider support:

1. GitHub Copilot CLI
2. **Crush**, the Charmbracelet coding agent. The UI may label the provider
   `Charm`, but code and process matching should use the canonical product name
   `crush`.
3. Pi coding agent (`@earendil-works/pi-coding-agent`)

Codex CLI, Claude Code, and Gemini CLI are secondary providers. Their
integrations should reuse the same state model after the primary providers are
working end-to-end.

## User-visible states

| State | Meaning |
|---|---|
| `Working` | The agent is processing a prompt, thinking, streaming a response, or executing tools. |
| `Blocked` | The agent is waiting for approval, permission, elicitation, or another explicit human decision. |
| `Done` | The current turn completed while the session was not being viewed. |
| `Idle` | The agent is ready for another command and the completed state has already been viewed. |

`Done` is not an agent lifecycle event. Agents generally report that a turn
stopped or became ready; multicrum decides between `Done` and `Idle`:

- If completion occurs while the session is focused in the active connection,
  set `Idle`.
- If completion occurs while it is not focused or its connection is inactive,
  set `Done`.
- Focusing a session in `Done` acknowledges it and changes it to `Idle`.
- Viewing does not clear `Working` or `Blocked`.

The internal model also needs `Unknown`, which is not displayed as an agent
state. It means presence was detected but no state source has produced a
trustworthy result yet.

## UI behavior

### Local TUI

In the left connection rail, show an aggregate agent state directly below the
existing session-count row:

```text
[1] backend
    3 sessions
    blocked Copilot
```

Only render the third row when that connection contains at least one detected
agent. Since connection entries become variable-height, rail layout must move
from the current fixed two-row capacity calculation to row-budgeted layout and
must generate hitboxes from the actual rendered bounds.

If a connection has multiple agent sessions, choose the aggregate state using:

```text
Blocked > Done > Working > Idle > Unknown
```

The priority favors states requiring user action. Put the lowercase state
before the provider so narrow panes preserve the most important text. Reserve
the first two columns for a one-character working spinner and its following
space so state text never shifts horizontally. Render `working` in yellow,
`blocked` in light pink, and `idle` in pale green; render the provider name
dimmer than the state. If more than one session has the selected state, render
a count when it fits, for example `  blocked Copilot (2)`.

Session tabs and selection dialogs should also expose a compact state marker so
the user can locate the affected session after seeing the connection-level
summary. Exact colors and glyphs are an implementation detail, but text must
remain available and color cannot be the only signal.

### Browser UI

Extend WebSocket `SessionInfo` with optional agent metadata:

```json
{
  "agent": {
    "provider": "copilot",
    "state": "blocked",
    "source": "hook"
  }
}
```

The browser should render the same per-session and aggregate connection state.
Older browsers ignore the new field, preserving protocol compatibility.

## Detection sources and precedence

Each update records:

```go
type AgentStatus struct {
    Provider   Provider
    State      State
    Source     Source
    Confidence Confidence
    UpdatedAt  time.Time
}
```

Sources, in descending authority:

1. **Native lifecycle hook or extension event**
2. **Structured terminal signal**, such as an OSC window-title state
3. **Provider-specific visible-screen heuristic**
4. **Process-tree presence detection**

A fresh higher-authority state must not be overwritten by a lower-authority
guess. Process polling normally establishes presence only; it does not infer
`Working` merely because the process consumes CPU or has child processes.

State updates need a generation/sequence number so a delayed poll or hook event
from a previous process cannot overwrite a newer session generation after
respawn.

## Primary provider integrations

### GitHub Copilot CLI hooks

The original assumption that Copilot CLI cannot report lifecycle state is no
longer correct. Current Copilot CLI releases support hooks including
`userPromptSubmitted`, `preToolUse`, `postToolUse`, `permissionRequest`,
`notification`, `agentStop`, `sessionStart`, and `sessionEnd`.

Relevant Copilot mapping:

| Copilot event | Multicrum result |
|---|---|
| `sessionStart` | Presence confirmed; initial state `Idle` unless another event follows. |
| `userPromptSubmitted` | `Working` |
| `preToolUse`, `postToolUse`, `postToolUseFailure` | `Working` |
| `permissionRequest` | `Blocked` |
| `notification: permission_prompt` | `Blocked` |
| `notification: elicitation_dialog` | `Blocked` |
| `agentStop` | Completion; resolve to `Done` or `Idle` based on focus. |
| `notification: agent_completed` | Completion; resolve to `Done` or `Idle`. |
| `notification: agent_idle` | Completion/ready; resolve to `Done` or `Idle`. |
| `sessionEnd` | Clear hook-derived presence after process confirmation. |
| `errorOccurred` | Record diagnostics; do not invent a new public state in v1. |

Copilot hook commands are synchronous for most lifecycle events, while
notifications are asynchronous. Multicrum's hook relay must therefore:

- read one JSON object from stdin;
- extract only a small allowlist of fields needed for state mapping;
- connect with a short timeout;
- write no stdout;
- exit successfully even if the multicrum owner is unavailable;
- never delay or alter the agent's permission/tool decision.

### Hook relay

Add an internal command:

```text
multicrum agent-event
```

The command reads the agent hook JSON from stdin and sends a compact event to
the owning multicrum daemon. It must not forward prompts, tool arguments,
responses, transcript contents, or notification messages.

Each PTY session receives inherited environment variables when its root process
is created:

```text
MULTICRUM_SERVER
MULTICRUM_SESSION_ID
MULTICRUM_SESSION_GENERATION
MULTICRUM_AGENT_ENDPOINT
MULTICRUM_AGENT_TOKEN
```

Use a stable runtime session ID, not the mutable zero-based session index.
Hooks and extensions inherit these variables even when the user launches an
agent from an interactive shell inside the session.

Reuse the private local owner endpoint by adding an `agent-event` client kind
and frame, or add a separate private endpoint if keeping input/control frames
isolated is simpler. Authenticate events with a per-owner random token inherited
through the session environment. Filesystem permissions alone are not enough
because any process running as the user can otherwise spoof another session.

Do not silently edit agent configuration during ordinary multicrum startup.
Provide an explicit future integration installer:

```text
multicrum agent-integrations install copilot
multicrum agent-integrations install crush
multicrum agent-integrations install pi
multicrum agent-integrations status
multicrum agent-integrations remove <provider>
```

For Copilot, install a dedicated user hook file under
`~/.copilot/hooks/multicrum.json`. The hook applies globally but
`multicrum agent-event` becomes a no-op when the `MULTICRUM_*` environment is
absent. Installation must merge safely, preserve unrelated configuration, and
support an exact rollback. V1 may document manual installation before adding
the installer.

Hook support is optional. Disabled, untrusted, policy-blocked, or older agents
must continue through the fallback detectors.

### Crush (Charm)

The coding agent is canonically named **Crush** and its executable is:

```text
crush
crush.exe
```

Do not use `charm` as the process matcher: Charm is the company/ecosystem name
and also refers to unrelated tools and services. Provider display text may say
`Charm/Crush` if that is clearer to users.

Crush is a Go/Bubble Tea application and normally runs as one process plus
children created by tool calls. It currently documents only one lifecycle
hook, `PreToolUse`. The hook environment includes `CRUSH=1`,
`AI_AGENT=crush`, `CRUSH_SESSION_ID`, `CRUSH_EVENT`, and tool metadata.

Relevant Crush mapping:

| Crush signal | Multicrum result |
|---|---|
| Process match | Presence confirmed; state `Unknown` until another source resolves it. |
| `PreToolUse` | `Working` |
| Permission UI screen fixture | `Blocked` |
| Active generation/spinner screen fixture | `Working` |
| Ready editor screen fixture after working | Completion; resolve to `Done` or `Idle`. |

The hook relay can be installed as a global `PreToolUse` entry in
`~/.config/crush/crush.json`, but it provides only a one-way transition to
`Working`. It cannot prove completion or idle. Do not infer completion from
hook silence.

`crush serve` exposes richer server/SSE state, including whether a workspace is
busy, but ordinary multicrum sessions run the standalone interactive TUI. A
future serve-mode adapter may consume that structured state when the user
explicitly launches Crush in server/client mode; it is not a v1 dependency.

Crush can emit native, OSC, or bell notifications for permission requests and
turn completion depending on configuration and terminal focus. Treat those as
optional structured signals only after real captures establish stable payloads.
They must not be required for basic support.

### Pi coding agent

Pi is the coding agent from the `earendil-works/pi` repository. Its executable
is normally:

```text
pi
pi.exe
```

The name is ambiguous: unrelated tools have also used the executable name
`pi`. A basename-only process match is therefore low-confidence. Some
installations may also expose `node`, `bun`, or another runtime as the process
executable. Raise confidence when command line or executable path identifies
`@earendil-works/pi-coding-agent`, or when the startup screen or Pi relay
extension confirms the provider.

Pi has the strongest primary integration surface: global TypeScript extensions
can subscribe to the interactive agent lifecycle without replacing the TUI or
changing how multicrum forwards PTY input/output.

Install a small global extension under:

```text
~/.pi/agent/extensions/multicrum-agent-state.ts
```

Relevant Pi extension mapping:

| Pi extension event | Multicrum result |
|---|---|
| `session_start` | Presence confirmed; initial `Idle`. |
| `before_agent_start`, `agent_start`, `turn_start` | `Working` |
| `tool_execution_start`, `tool_execution_update`, `tool_execution_end` | `Working` |
| `agent_end` | Do not transition; retry, compaction, or queued follow-up work may still start. |
| `agent_settled` | Authoritative completion; resolve to `Done` or `Idle`. |
| `session_shutdown` | Clear extension-derived presence after process confirmation. |

`agent_settled` is preferable to `agent_end`: it fires only when no retry,
compaction, steering message, or follow-up remains.

Pi does not include a built-in permission system, and arbitrary extensions can
replace the editor with their own question/confirmation UI. There is no general
documented event meaning "any extension is waiting for human input."
Consequently, `Blocked` is best-effort for Pi and requires:

- a future explicit relay API adopted by question/permission extensions; or
- narrowly tested visible-screen patterns for known Pi UI components.

Pi also supports `--mode json`, `--mode rpc`, and an SDK. RPC exposes
`agent_start`, `agent_settled`, tool events, and `get_state.isStreaming`, but it
is not a passive side channel for an already-running interactive Pi TUI.
Multicrum must not silently replace interactive Pi with RPC mode. RPC is useful
for tests and for a possible future managed Pi frontend, which is outside this
black-box PTY feature.

## Process-tree presence detection

Default poll interval: **2 seconds**, configurable.

For every poll:

1. Take one operating-system process snapshot for the whole multicrum server.
2. Build a `PPID -> children` index.
3. For every local session root PID, walk its descendant tree.
4. Match provider definitions against executable basename and, only where
   necessary, command-line identity.
5. Publish presence changes through Bubble Tea messages; never mutate UI state
   from the poll goroutine.

Initial primary-provider matchers:

```text
copilot, copilot.exe
crush, crush.exe
pi, pi.exe
```

Matching is case-insensitive on Windows. The root process itself must be
included because a session can start directly as the agent rather than starting
it from a shell. Pi requires the additional confidence rules described above.

The process abstraction should expose PID, parent PID, executable name,
command line, and start time. Start time prevents PID reuse from attaching old
state to a replacement process. Platform implementations may use `/proc` on
Linux, `sysctl`/libproc on macOS, and Toolhelp APIs on Windows, or use
`github.com/shirou/gopsutil/v4/process` behind a narrow internal interface.
Tests must use a fake inventory rather than real system processes.

If process enumeration fails due to permissions or platform limitations,
report detection as unavailable rather than concluding that no agent exists.

### SSH-backed sessions

Multicrum cannot enumerate the remote process tree of a session backed by
`pkg/ssh_client`; locally it sees only its own SSH transport. For these sessions:

- local process polling is disabled;
- terminal signals and screen heuristics remain available;
- native hooks require a future authenticated remote relay/tunnel;
- v1 must document remote agent presence as best-effort.

This limitation does not apply when multicrum itself runs on the remote host,
because those PTY children are local to that multicrum daemon.

## Provider-specific screen fallbacks

Screen scraping is a compatibility fallback, not the primary interface. It is
enabled only after the process detector has identified a provider candidate,
preventing ordinary terminal text from being mistaken for agent UI.

Use the current `VTScreen` emulator state, not raw PTY chunks. Agent TUIs redraw
in place with cursor movement and erase sequences, so stream substring matching
is incorrect.

Evaluate a small bottom region of the visible screen (for example, the last
four non-empty rows) after a render tick and after presence detection changes.
Strip ANSI styling but preserve row boundaries and significant spaces.

### Copilot

### Working

Match stable fragments, not model names, counters, or the whole line:

```text
Working ·
esc interrupt
```

Both fragments must appear in the footer region. This accommodates changing
token counts and model labels such as:

```text
Working · 26.1 KiB esc interrupt             GPT-5.6 Sol
```

### Blocked

Match an interactive selection/help row containing stable controls:

```text
enter to select
esc to cancel
```

and require an adjacent horizontal divider or another provider-specific
approval/question marker. Do not classify on a divider alone.

Additional approval, permission, and question captures should be added as test
fixtures before broadening the matcher. Patterns must be provider-versioned
data, not scattered through UI update code.

### Completion and idle fallback

The disappearance of `Working` alone is not completion: it can occur during a
redraw or resize. A fallback transition from `Working`/`Blocked` to completion
requires a recognized ready/input footer for two consecutive observations or a
short debounce window with no subsequent working marker.

Until reliable Copilot ready-screen captures are committed, the fallback may
leave the state at `Unknown` rather than manufacture `Done`. False negatives
are preferable to false completion notifications.

Every heuristic needs:

- a minimum supported agent version or fixture provenance;
- positive and negative screen fixtures;
- behavior at narrow and wide terminal sizes;
- resize and alternate-screen coverage;
- a confidence level and last-match timestamp.

### Crush

Crush is itself a Bubble Tea TUI, so use the same emulator-derived footer
strategy as Copilot:

- capture real working, permission, question, ready-editor, notification, and
  error screens;
- match layout-independent labels and controls rather than colors, spinners,
  token counts, models, or full rows;
- require a provider-specific combination of rows before reporting `Blocked`;
- debounce ready-editor detection before converting a previous `Working` state
  to completion.

The `PreToolUse` hook can establish `Working` immediately before a tool call,
while screen fixtures cover model streaming, permission prompts, and
completion. Until ready and blocked fixtures are committed, leave those states
`Unknown` instead of borrowing Copilot patterns.

### Pi

When the global Pi extension is installed, screen scraping should normally be
unnecessary for `Working` and completion. Screen fallback is still useful for:

- confirming low-confidence `pi` process-name matches;
- installations where extensions are disabled or untrusted;
- known question/selection UIs that need `Blocked`.

Pi uses a scrollback-appending differential TUI rather than a conventional
full-screen viewport. Fixtures must therefore test both current visible rows
and redraw sequences. Do not infer idle merely from the static footer; the
authoritative extension event is `agent_settled`.

## OSC and structured terminal signals

The VT byte path should expose parsed OSC title changes as structured events
without removing them from raw history or browser output.

Gemini CLI currently enables `ui.dynamicWindowTitle` by default and documents:

| Title icon | State |
|---|---|
| `✦` | `Working` |
| `✋` | `Blocked` |
| `◇` | `Idle`/ready |

This is a stronger fallback than screen scraping and should be implemented as a
future provider source. OSC parsing must handle BEL and ST terminators and
sequences split across PTY reads. Do not use a regular expression over
individual chunks.

## Architecture

Add a provider-neutral package:

```text
pkg/agentdetect/
    state.go          # Provider, State, Source, AgentStatus
    monitor.go        # registration, polling, precedence, generations
    process.go        # process inventory interface
    process_linux.go
    process_darwin.go
    process_windows.go
    providers.go      # provider registry
    copilot.go        # Copilot process, screen, and hook mapping
    crush.go          # Crush process, screen, and PreToolUse mapping
    pi.go             # Pi process, screen, and extension event mapping
    events.go         # normalized hook/extension lifecycle events
```

Recommended ownership:

- One `agentdetect.Monitor` per multicrum owner, not one polling goroutine per
  session.
- Sessions register a stable ID, generation, local root PID, backend type, and
  a callback/snapshot accessor.
- The monitor performs one process snapshot per interval and evaluates only
  registered sessions.
- Results enter the Bubble Tea loop as an `agentStatusMsg`.
- UI state stores status by stable session ID. Index-based maps are unsafe
  because kill and move operations reindex sessions.
- Add/remove/respawn/connection deletion must register, update, or unregister
  monitor entries.
- State changes call `notifyMeta()` so TUI and browser labels stay synchronized.

`Session` needs thread-safe accessors for its root PID, stable runtime ID,
generation, and backend kind. Do not expose the mutable fields directly.

## Configuration

Proposed YAML:

```yaml
agentDetection:
  enabled: true
  processPolling: true
  pollInterval: 2s
  screenHeuristics: true
  hooks: true
  spinnerAnimation: true
  spinnerStyle: rectangle
  providers:
    copilot:
      enabled: true
    crush:
      enabled: true
    pi:
      enabled: true
    codex:
      enabled: false
    claude:
      enabled: false
    gemini:
      enabled: false
```

Defaults:

- feature enabled;
- process polling enabled;
- two-second interval;
- hook receiver enabled, but no agent files are installed implicitly;
- screen heuristics enabled for recognized providers;
- working-state spinner animation enabled; set
  `agentDetection.spinnerAnimation: false` to keep the marker static;
- `agentDetection.spinnerStyle` accepts `rectangle` (the default
  `⠋ ⠙ ⠹ ⠸ ⠼ ⠴ ⠦ ⠧ ⠇ ⠏` sequence) or `circle`
  (`● ◉ ◎ ○`);
- the global actions menu includes `Settings` immediately before `Quit`; its
  settings pane changes spinner style and animation at runtime, updates browser
  metadata immediately, and persists to the configured layout file;
- Copilot, Crush, and Pi registered as primary providers;
- Codex, Claude, and Gemini disabled until their secondary implementation
  phase is complete.

Reject non-positive intervals. Clamp extremely short intervals to a safe
minimum such as 250 ms. Configuration loading must preserve compatibility with
files that omit the new section.

## State-machine rules

1. No matched process and no fresh authoritative event: no detected agent.
2. Process appears: provider present, state `Unknown` until another source or a
   provider-defined initial state resolves it.
3. Working event always changes `Blocked`, `Done`, or `Idle` to `Working`.
4. Blocked event changes `Working` or `Unknown` to `Blocked`.
5. Completion event changes to `Idle` if currently viewed, otherwise `Done`.
6. Focus acknowledgement changes only `Done` to `Idle`.
7. Process disappearance starts a short grace period to survive process
   replacement during upgrades/re-exec. Clear presence when the grace expires.
8. Respawn increments generation and clears all previous detection state.
9. Stale events, mismatched tokens, unknown stable IDs, and older generations
   are discarded.
10. A lower-precedence source cannot override a fresh higher-precedence source.
    After a provider-specific freshness timeout, fallback sources may resume.

## Security and privacy

- Never transmit or retain prompts, model responses, tool arguments, tool
  results, transcript paths, or notification text for this feature.
- Treat project-provided hook configuration as untrusted. Multicrum must not
  install project hooks automatically.
- The relay endpoint is local-only and authenticated with an owner-generated
  token.
- Hook commands must not grant permissions, block tools, or return agent
  control decisions.
- Do not expose tokens in `list`, `status`, logs, WebSocket metadata, config
  saves, or process arguments.
- Limit event size before JSON decoding and use read deadlines.
- Rate-limit malformed or excessive events per session.

## Implementation plan

### Phase 1: Core state model and process presence

1. Add stable runtime IDs and PID/backend/generation accessors to `Session`.
2. Add `pkg/agentdetect` state types, provider registry, fakeable process
   inventory, and one owner-level monitor.
3. Implement local Copilot, Crush, and Pi process matching with the two-second
   configurable poll and Pi's additional confidence checks.
4. Wire registration through new, respawn, move, kill, and connection removal.
5. Add `agentStatusMsg`, per-session storage, aggregate connection state, and
   focus acknowledgement.
6. Extend WebSocket metadata and render TUI/browser state.

This phase can report presence and `Unknown`, but must not infer `Idle` or turn
completion without a state source.

### Phase 2: Primary structured integrations

1. Add the authenticated agent-event protocol and `multicrum agent-event`
   helper.
2. Inject per-session routing environment variables into local PTY children.
3. Normalize Copilot hook payloads into provider-neutral events.
4. Implement the global Pi extension relay and map `agent_settled` as the
   authoritative completion event.
5. Implement the Crush `PreToolUse` relay as an authoritative `Working` event,
   while documenting that it cannot report completion.
6. Add documented manual integration setup before implementing installers.

### Phase 3: Primary screen and terminal fallbacks

1. Add a bottom-screen snapshot API that returns plain visible rows from the
   same rendered generation.
2. Implement data-driven Copilot footer matchers for `Working` and `Blocked`.
3. Capture and implement Crush working, permission, and ready-editor fixtures.
4. Add Pi startup/ready fixtures for presence confirmation and known blocked
   UIs, without overriding fresh extension events.
5. Capture OSC/native notification sequences before treating them as state
   sources.
6. Add provider-specific debounce, confidence, and freshness handling.
7. Resolve heuristic completion only after a tested ready-screen signature
   exists for that provider.

Native hook/extension events remain authoritative over screen fallbacks.

### Phase 4: Integration management

1. Add `agent-integrations install/status/remove`.
2. Safely merge and roll back Copilot hook files, Crush global hook config, and
   the Pi global extension.
3. Detect disabled, changed, untrusted, or policy-blocked integrations and show
   a non-intrusive diagnostic.
4. Version installed relay files so upgrades are explicit and reversible.

### Phase 5: Secondary providers

1. Codex CLI: lifecycle hooks (`UserPromptSubmit`, `PreToolUse`,
   `PermissionRequest`, `Stop`, and related events).
2. Claude Code: lifecycle hooks, using the same normalized event vocabulary.
3. Gemini CLI: OSC title state first, then hooks.
4. Add provider-specific process identities and fixtures without changing the
   core monitor or UI state model.

### Phase 6: Remote hook bridge

Design an authenticated relay for SSH-backed sessions. It must not require
polling a second SSH command every two seconds. Candidate designs include a
dedicated SSH channel or an explicitly configured forwarded local endpoint.
This phase is out of scope for the first release.

## Test plan

### Unit tests

- Process-tree traversal detects direct and descendant Copilot, Crush, and Pi
  processes.
- Ambiguous `pi` processes remain low-confidence until corroborated.
- Unrelated same-name processes outside the session tree do not match.
- PID reuse and generation changes discard stale state.
- Inventory errors produce unavailable/unknown, not false absence.
- Source precedence and freshness behave deterministically.
- Every state-machine transition, including `Done -> Idle` on focus.
- Aggregate connection priority and counts.
- Hook payload normalization and rejection of unknown/oversized/stale events.
- OSC parser handles split sequences, BEL/ST terminators, and malformed input.

### UI tests

- Variable-height connection rail entries fit, scroll, and produce correct
  hitboxes.
- Agent state follows session moves, kills, respawns, and connection moves.
- A completion in a background connection shows `Done`.
- Focusing the completed session changes only that session to `Idle`.
- Web metadata and browser rendering match TUI state.

### Fixture tests

- Record real Copilot, Crush, and Pi PTY output with `cmd/ptyrec`.
- Store minimal sanitized byte fixtures, not transcripts or prompts.
- Replay fixtures at multiple widths and assert the emulator-derived state.
- Include negative fixtures containing words such as `Working` and
  `enter to select` in normal shell output.

### Integration tests

- Start fake shell children that later exec fake primary-provider processes.
- Send synthetic authenticated hook events and verify UI/meta transitions.
- Load the Pi relay extension in an integration fixture and verify
  `agent_start -> Working` and `agent_settled -> Done/Idle`.
- Verify a Crush `PreToolUse` event reports `Working` but hook silence does not
  report completion.
- Verify hook relay failure does not block or alter the child.
- Verify daemon shutdown stops the monitor and does not leak poll goroutines.

### Performance tests

- One process inventory per owner interval, independent of session count.
- Benchmark traversal with at least 100 sessions and a realistic process tree.
- Screen heuristics inspect only a bounded footer region and run on coalesced
  render ticks, not on every PTY write.

## Acceptance criteria for the first primary-provider release

- Copilot, Crush, and Pi are detected whether they are the session root or
  launched from a shell.
- Detection does not match an agent process belonging to another session.
- An unrelated `pi` executable is not promoted to a high-confidence Pi coding
  agent without corroboration.
- Copilot `Working` and `Blocked` states are recognized from committed real PTY
  fixtures.
- Crush reports `Working` from `PreToolUse`; completion is reported only from a
  tested ready-screen or structured signal.
- Pi reports `Working` from `agent_start` and completion from
  `agent_settled`; its `Blocked` state may remain best-effort.
- Native Copilot hooks and the Pi extension override screen heuristics.
- A background completion becomes `Done`; focusing it changes it to `Idle`.
- Connection rail and browser metadata remain correct after session reindexing.
- Polling remains bounded and does not add a goroutine per session.
- Unsupported and SSH-backed cases degrade to no state rather than false state.

## Research references

- GitHub Copilot hooks overview:
  <https://docs.github.com/en/copilot/concepts/agents/hooks>
- GitHub Copilot hooks reference and event payloads:
  <https://docs.github.com/en/copilot/reference/hooks-reference>
- Crush repository:
  <https://github.com/charmbracelet/crush>
- Crush lifecycle hooks:
  <https://github.com/charmbracelet/crush/tree/main/docs/hooks>
- Pi coding agent repository:
  <https://github.com/earendil-works/pi>
- Pi extension lifecycle:
  <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/extensions.md>
- Pi RPC protocol and structured state:
  <https://github.com/earendil-works/pi/blob/main/packages/coding-agent/docs/rpc.md>
- Claude Code hook lifecycle:
  <https://code.claude.com/docs/en/hooks>
- Codex hooks:
  <https://learn.chatgpt.com/docs/hooks>
- Codex configuration and hook locations:
  <https://learn.chatgpt.com/docs/config-file/config-advanced#hooks>
- Gemini CLI hooks:
  <https://geminicli.com/docs/hooks/>
- Gemini CLI hook schemas:
  <https://github.com/google-gemini/gemini-cli/blob/main/docs/hooks/reference.md>
- Gemini CLI dynamic title and notification configuration:
  <https://geminicli.com/docs/reference/configuration/>
- Cross-platform Go process information:
  <https://pkg.go.dev/github.com/shirou/gopsutil/v4/process>
