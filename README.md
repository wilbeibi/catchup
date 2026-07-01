<div align="center">

# catchup

### Handoff-ready context of your AI coding-agent sessions

Switch between Claude Code, Codex, OpenCode & Pi Agent without re-explaining everything.

[![CI](https://github.com/wilbeibi/catchup/actions/workflows/ci.yml/badge.svg)](https://github.com/wilbeibi/catchup/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wilbeibi/catchup.svg)](https://pkg.go.dev/github.com/wilbeibi/catchup)

</div>

You jump between coding agents — Claude Code, Codex, OpenCode, Pi Agent — each keeping its history in a different place and format. **catchup** reads any of them and prints a clean, handoff-ready summary — just the conversation, no tool-call noise — so the next agent (or you, days later) picks up instantly.

- **One command, any agent.** Run `catchup <agent>` for `codex`, `claude`, `opencode`, or `pi-agent` — same clean output.
- **Just the conversation.** User and assistant messages only. Tool calls, reasoning, and token noise are stripped.
- **Built for handoff.** Pipe it to the next agent or read it yourself days later — no re-briefing.

<div align="center">

**Claude Code hits its 5-hour limit — Codex runs `catchup` in the same folder and keeps going, no re-explaining.**

<img src="assets/handoff.gif" alt="A second agent picks up a Claude Code session that hit its 5-hour limit by running catchup, instead of being re-briefed" width="850">

**Ask a new agent about earlier work — it runs `catchup -q` to find the old session and pull it back into context.**

<img src="assets/recall.gif" alt="An agent recalls a past session by keyword with catchup -q, then pulls it up by id" width="850">

</div>

## Install

```bash
go install github.com/wilbeibi/catchup@latest
```

```bash
catchup install-skill          # writes SKILL.md to every detected agent's skills directory
catchup install-skill codex    # or target one agent
```

Restart the agent, then ask it to "catch up on the last session".

## What it does

Default output is clean Markdown: session metadata plus the user/assistant conversation, with tool calls and reasoning removed. Pipe it, save it, or hand it to another agent.

## Usage

Use `<agent>` as `codex`, `claude`, `opencode`, or `pi-agent`.

### Read a session

```bash
catchup <agent>   # latest session for that agent in this directory
```

### Fork the latest session

```bash
catchup fork                 # fork the newest session in this directory, across agents
catchup fork <agent>         # fork that agent's newest session in this directory
```

`fork` dispatches to the agent's native fork command, so the new agent keeps
real session context instead of receiving a rendered handoff transcript.

### Install the skill

```bash
catchup install-skill          # every detected agent
catchup install-skill claude   # just one
```

Writes `SKILL.md` to each agent's own skills directory so it can invoke
`catchup` on its own — no manual copy-paste.

### Find the right session

```bash
catchup codex --list                   # list sessions in this directory
catchup codex -q "auth"                # search sessions in this directory
catchup codex/3                        # 3rd most recent session in this directory
catchup claude --id <session-id>       # exact session id from any directory
```

### Limit the output

```bash
catchup codex --last 4                 # last 4 exchanges
catchup claude --since-compact         # final compaction segment, if any
catchup codex --info                   # metadata only
```

### Change the format

```bash
catchup codex --md                     # Markdown output
catchup codex --html                   # self-contained HTML
catchup codex --json                   # structured JSON
```

## Reference

| Argument / flag | What it does |
|---|---|
| `<agent>` | latest session for this agent in the current directory |
| `<agent>/N` | N-th most recent session in the current directory |
| `--list` | list sessions in the current directory |
| `-q, --query <text>` | filter current-directory sessions by keyword (implies `--list`) |
| `--id <id>` | select an exact session by id, ignoring the directory filter |
| `--info` | metadata only, no messages |
| `--last <N>` | keep only the last N exchanges |
| `--since-compact` | keep the final compaction segment, or the whole session if none |
| `-n, --limit <N>` | cap listing rows (default 20) |
| `--md` · `--html` · `--json` | output format (default `--md`) |
| `-h, --help` | print usage |

Forking:

| Command | What it does |
|---|---|
| `fork` | fork the newest session in the current directory across all agents |
| `fork <agent>` | fork the newest current-directory session for one agent |

Installing the skill:

| Command | What it does |
|---|---|
| `install-skill` | write `SKILL.md` to every detected agent's skills directory |
| `install-skill <agent>` | write `SKILL.md` to one agent's skills directory |

## Supported agents

- Codex
- Claude Code
- OpenCode
- Pi Agent

## What catchup doesn't do

**No cross-agent mixing.** You pick one agent at a time. There's no unified "show me everything everywhere" view. Each agent's sessions have different shapes and mixing them would flatten meaningful differences.

**No raw replay.** If you need every tool call, result, and reasoning step for debugging, catchup is the wrong tool — grep the raw `.jsonl` or `.db` files directly.

**No writing.** catchup is read-only. It doesn't modify, delete, or tag sessions.
The `fork` subcommand is the exception: it launches the selected agent's native
fork command.

## License

MIT
