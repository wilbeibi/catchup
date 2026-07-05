<div align="center">

# catchup

### Pick up a coding agent session without starting over

</div>

`catchup` is for the moment an agent hits a limit and you don't want to explain the whole job again. It pulls the useful part of the local session into clean Markdown. `catchup fork` picks the work back up in the same agent or a different one.

Use it when you switch tools, pick up older work, or want a clean record of what happened.

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

I use `catchup` with [herdr](https://herdr.dev) day to day. The [wilbeibi/herdr-catchup](https://github.com/wilbeibi/herdr-catchup) plugin adds pane actions for summary, fork, and handoff:

```bash
herdr plugin install wilbeibi/herdr-catchup
```

## Usage

Use `<agent>` as `codex`, `claude`, `opencode`, or `pi-agent`. Omit it and catchup uses whichever agent has the newest session in this directory. Inside a live session, that's usually the session you're in.

**For you:** run in your terminal to re-enter a session:

```bash
catchup fork                     # fork the newest session across agents
catchup fork <agent>             # fork that agent's newest session
catchup fork codex --into claude # continue a Codex session in Claude
```

**For agents:** run inside a session to read prior work:

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

Use `fork` to continue with the same agent and keep native session state. Use `fork --into` to start another agent with the transcript. Use read commands when you want old work in a clean context.

## Boundaries

- One agent at a time. It does not merge histories.
- Conversation only. It strips tool calls, command output, and reasoning traces.
- Read-only, except `fork`.
- Same-agent `fork` uses the agent's native resume path, so it keeps real session state.
- Cross-agent `fork --into` seeds the new agent with a transcript, not native state.

## License

MIT
