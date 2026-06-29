<div align="center">

# catchup

### Handoff-ready summaries of your AI coding-agent sessions

Switch between Claude Code, Codex, OpenCode & Pi Agent without re-explaining everything.

[![Go Reference](https://pkg.go.dev/badge/github.com/wilbeibi/catchup.svg)](https://pkg.go.dev/github.com/wilbeibi/catchup)
[![Go Report Card](https://goreportcard.com/badge/github.com/wilbeibi/catchup)](https://goreportcard.com/report/github.com/wilbeibi/catchup)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

</div>

You jump between coding agents — Claude Code, Codex, OpenCode, Pi Agent — each keeping its history in a different place and format. **catchup** reads any of them and prints a clean, handoff-ready summary — just the conversation, no tool-call noise — so the next agent (or you, days later) picks up instantly.

- **One command, any agent.** `catchup claude`, `catchup codex`, `catchup opencode`, `catchup pi-agent` — same clean output.
- **Just the conversation.** User and assistant messages only. Tool calls, reasoning, and token noise are stripped.
- **Built for handoff.** Pipe it to the next agent or read it yourself days later — no re-briefing.

<div align="center">

<img src="assets/handoff.gif" alt="A second agent picks up a Claude Code session that hit its 5-hour limit by running catchup, instead of being re-briefed" width="850">

<sub><i>Claude Code hits its 5-hour limit (left); Codex — in the same directory — runs <code>catchup</code> to pick up the thread and keep going (right). No re-explaining.</i></sub>

</div>

## Install

```bash
go install github.com/wilbeibi/catchup@latest
```

Requires Go 1.25+. Single binary, one dependency (SQLite for OpenCode, pure Go).

## What it does

```bash
catchup codex        # latest Codex session → clean Markdown to stdout
catchup claude       # latest Claude session
catchup opencode     # latest OpenCode session
catchup pi-agent     # latest Pi Agent session
```

The output is YAML frontmatter (who, when, where, which model) followed by the conversation timeline — user and assistant messages only, numbered and timestamped. Tool calls, reasoning, and bookkeeping noise are gone. Compaction markers stay as lightweight breaks.

```
---
provider: codex
session: sess-abc123
updated: 2026-06-26T14:30:00Z
cwd: /home/you/src/app
model: claude-sonnet-4-20250514
---
## 1. user | 2026-06-26 14:25
Refactor the auth middleware to use the new token format

## 2. assistant | 2026-06-26 14:26
Here's the updated middleware...
```

That's it. You can pipe it, save it, or hand it to another agent. When there's nothing to read, catchup says why on stderr and exits non-zero:

```
catchup: claude: no sessions found under ~/.claude
```

## Finding sessions

```bash
catchup codex --list                    # list recent sessions
catchup codex -q "auth"                 # search for sessions about auth
catchup codex/3                         # 3rd most recent session
catchup claude --id <exact-session-id>  # exact session (for scripts)
```

<div align="center">

<img src="assets/recall.gif" alt="An agent recalls a past session by keyword with catchup -q, then pulls it up by id" width="850">

<sub><i>Ask opencode about earlier work in Claude — it runs the <code>catchup</code> skill to recall the session that solved it.</i></sub>

</div>

## Other output formats

```bash
catchup codex -I      # metadata only (who/when/where, no conversation)
catchup codex --last 4       # last 4 exchanges only (each = your prompt + the reply)
catchup claude --since-compact # only the final compaction segment (leads with Claude's recap)
catchup codex --html   # self-contained HTML (for sharing in a browser)
catchup codex --json   # structured JSON (for scripts)
```

## Options

| Argument / flag | What it does |
|---|---|
| `<provider>` | one of `codex`, `claude`, `opencode`, `pi-agent` |
| `<provider>/N` | the N-th most recent session (`/1` = latest) |
| `--list` | list recent sessions as a table |
| `-q, --query <text>` | filter sessions by keyword (implies `--list`) |
| `--id <id>` | select an exact session by id (ignores the directory filter) |
| `-I, --info` | metadata only, no messages |
| `--last <N>` | keep only the last N exchanges |
| `--since-compact` | keep only the final compaction segment |
| `-n, --limit <N>` | cap listing rows (default 20) |
| `--md` · `--html` · `--json` | output format (default `--md`) |
| `-h, --help` | print usage |

## Supported agents

catchup reads sessions from Codex, Claude Code, OpenCode, and Pi Agent. It finds them automatically under `~/.codex`, `~/.claude`, `~/.local/share/opencode`, and `~/.pi/agent`. Each can be overridden with an environment variable: `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `XDG_DATA_HOME`, `PI_CODING_AGENT_DIR`.

**What's included:** user and assistant messages, session metadata (title, directory, model, branch), compaction markers.

**What's skipped:** tool calls, tool results, reasoning/thinking blocks, token counts, file snapshots.

## Use it from an agent

catchup ships with an agent skill (`skill.md`), so a coding agent can run it on its own — tell it "catch up on the last session" or "I switched agents" and it pulls the prior context in before continuing. Works across Claude Code, Codex, OpenCode, and Pi Agent.

## What catchup doesn't do

**No cross-agent mixing.** You pick one agent at a time. There's no unified "show me everything everywhere" view. Each agent's sessions have different shapes and mixing them would flatten meaningful differences.

**No raw replay.** If you need every tool call, result, and reasoning step for debugging, catchup is the wrong tool — grep the raw `.jsonl` or `.db` files directly.

**No writing.** catchup is read-only. It doesn't modify, delete, or tag sessions.

## Troubleshooting

**`catchup: <provider>: no sessions found under …`** — that agent has no history where catchup looks. Defaults are `~/.codex`, `~/.claude`, `~/.local/share/opencode`, `~/.pi/agent`; override with `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `XDG_DATA_HOME`, or `PI_CODING_AGENT_DIR`.

**`--list` is empty or shows the wrong session** — `--list`, `-q`, and `/N` only consider sessions whose working directory matches where you run catchup. Run it from the project directory, or use `--id <id>` to reach any session.

**Selecting a session in a script** — use `--id <full-id>`, not `/N`: ranks shift as new sessions appear, ids are stable.

## License

MIT
