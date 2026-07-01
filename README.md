<div align="center">

# catchup

### Handoff-ready context of your AI coding-agent sessions

Switch between Claude Code, Codex, OpenCode & Pi Agent without re-explaining everything.

[![CI](https://github.com/wilbeibi/catchup/actions/workflows/ci.yml/badge.svg)](https://github.com/wilbeibi/catchup/actions/workflows/ci.yml)
[![Go Reference](https://pkg.go.dev/badge/github.com/wilbeibi/catchup.svg)](https://pkg.go.dev/github.com/wilbeibi/catchup)

</div>

You jump between coding agents — Claude Code, Codex, OpenCode, Pi Agent — each keeping its history in a different place and format. **catchup** reads any of them and prints a clean, handoff-ready summary — just the conversation, no tool-call noise — so the next agent (or you, days later) picks up instantly.

- **One command, any agent.** `catchup claude`, `catchup codex`, `catchup opencode`, `catchup pi-agent` — same clean output.
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

<details>
<summary>Add skill to Claude Code</summary>

```bash
mkdir -p ~/.claude/skills/catchup
curl -fsSL https://raw.githubusercontent.com/wilbeibi/catchup/main/SKILL.md \
  -o ~/.claude/skills/catchup/SKILL.md
```

</details>

<details>
<summary>Add skill to Codex</summary>

```bash
mkdir -p ~/.agents/skills/catchup
curl -fsSL https://raw.githubusercontent.com/wilbeibi/catchup/main/SKILL.md \
  -o ~/.agents/skills/catchup/SKILL.md
```

</details>

<details>
<summary>Add skill to OpenCode</summary>

OpenCode uses Claude Code skills by default.

```bash
mkdir -p ~/.claude/skills/catchup
curl -fsSL https://raw.githubusercontent.com/wilbeibi/catchup/main/SKILL.md \
  -o ~/.claude/skills/catchup/SKILL.md
```

</details>

<details>
<summary>Add skill to Pi Agent</summary>

```bash
mkdir -p ~/.pi/agent/skills/catchup
curl -fsSL https://raw.githubusercontent.com/wilbeibi/catchup/main/SKILL.md \
  -o ~/.pi/agent/skills/catchup/SKILL.md
```

</details>

Restart the agent, then ask it to "catch up on the last session".

## What it does

Default output is clean Markdown: session metadata plus the user/assistant conversation, with tool calls and reasoning removed. Pipe it, save it, or hand it to another agent.

## Usage

### Read a session

```bash
catchup codex      # latest Codex session in this project
catchup claude     # latest Claude Code session in this project
catchup opencode   # latest OpenCode session in this project
catchup pi-agent   # latest Pi Agent session in this project
```

### Fork the latest session

```bash
catchup fork                 # fork the newest session in this project, across agents
catchup fork codex           # fork the newest Codex session in this project
catchup fork claude          # fork the newest Claude Code session in this project
catchup fork opencode        # fork the newest OpenCode session in this project
catchup fork pi-agent        # fork the newest Pi Agent session in this project
```

`fork` dispatches to the agent's native fork command, so the new agent keeps
real session context instead of receiving a rendered handoff transcript.

### Find the right session

```bash
catchup codex --list                   # list sessions in this project
catchup codex -q "auth"                # search sessions in this project
catchup codex/3                        # 3rd most recent session in this project
catchup claude --id <session-id>       # exact session id from any project
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
| `<provider>` | latest session for this provider in the current directory |
| `<provider>/N` | N-th most recent session in the current directory |
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
| `fork` | fork the newest session in the current directory across all providers |
| `fork <provider>` | fork the newest current-directory session for one provider |

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
