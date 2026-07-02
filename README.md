<div align="center">

# catchup

### Hand a coding-agent session to the next agent — or read it as clean Markdown

</div>

`catchup` reads local agent history and prints the conversation: user and assistant messages, with tool calls and reasoning traces removed. `catchup fork` re-enters a session — natively for the same agent, or seeding a different agent with the transcript.

Use it when an agent hits a limit, you switch tools, or you need to recall what happened in an older session.

<div align="center">

**Claude Code hits its usage limit. One command hands the session to Codex.**

<img src="assets/handoff.gif" alt="Claude Code hits its 5-hour limit; catchup fork --into codex starts Codex seeded with the Claude session's transcript" width="850">

**For older work, search sessions by keyword and open the matching one.**

<img src="assets/recall.gif" alt="An agent recalls a past session by keyword with catchup -q, then pulls it up by id" width="850">

</div>

## Install

```bash
go install github.com/wilbeibi/catchup@latest

catchup install-skill          # optional: install the agent skill
catchup install-skill <agent>  # ...or for one agent only
```

Restart the agent, then ask it to catch up on the last session.

## Usage

Use `<agent>` as `codex`, `claude`, `opencode`, or `pi-agent`. Omit it and catchup uses whichever agent has the newest session in this directory — inside a live session, that's usually the session you're in.

**For you** — run in your terminal to re-enter a session:

```bash
catchup fork                     # fork the newest session across agents
catchup fork <agent>             # fork that agent's newest session
catchup fork codex --into claude # continue a Codex session in Claude
```

**For agents** — run inside a session to read prior work:

```bash
catchup <agent> --since-compact  # another agent's latest, since compaction
catchup --since-compact          # this session's context after a compaction
catchup <agent> --list           # list recent sessions
catchup <agent> -q "auth"        # search sessions
catchup <agent>/3                # read 3rd newest session
catchup <agent> --id <id>        # read exact session

catchup <agent> --last 4         # read last 4 exchanges
catchup <agent> --json           # render JSON; also --html
```

Same agent, continuing where it left off → `fork` (native, full state). Crossing agents → `fork --into`, which starts the other agent with the transcript as its first prompt. Recalling old work or starting a clean context → read.

## Boundaries

- One agent at a time. It does not merge histories.
- Conversation only. It strips tool calls, command output, and reasoning traces.
- Read-only, except `fork`. Same-agent fork preserves context cache. Dispatches to native fork, so sessions inherit real context, not a handoff transcript. `fork --into` is the exception that crosses agents: it seeds the new agent with the transcript — conversation, not native state.

## License

MIT
