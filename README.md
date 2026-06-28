<div align="center">

# catchup

### Handoff-ready summaries from your agent sessions

**Single binary · 100% local · Works offline**

</div>

## The problem

You use multiple coding agents — Codex, Claude Code, OpenCode, Pi Agent. Each keeps its conversation history in a different place, in a different format. When you need to hand off a session's context to another agent, you open the old UI, scroll through walls of tool calls and reasoning blocks, find the actual messages, copy a chunk, and paste.

catchup reads local agent histories and gives you a clean, compact summary: just the conversation, stripped of noise.

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

That's it. You can pipe it, save it, or hand it to another agent.

## Finding sessions

```bash
catchup codex --list                    # list recent sessions
catchup codex -q "auth"                 # search for sessions about auth
catchup codex/3                         # 3rd most recent session
catchup claude --id <exact-session-id>  # exact session (for scripts)
```

## Other output formats

```bash
catchup codex -I      # metadata only (who/when/where, no conversation)
catchup codex --last 4       # last 4 exchanges only (each = your prompt + the reply)
catchup claude --since-compact # only the final compaction segment (leads with Claude's recap)
catchup codex --html   # self-contained HTML (for sharing in a browser)
catchup codex --json   # structured JSON (for scripts)
```

## Supported agents

catchup reads sessions from Codex, Claude Code, OpenCode, and Pi Agent. It finds them automatically under `~/.codex`, `~/.claude`, `~/.local/share/opencode`, and `~/.pi/agent`. Each can be overridden with an environment variable: `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `XDG_DATA_HOME`, `PI_CODING_AGENT_DIR`.

**What's included:** user and assistant messages, session metadata (title, directory, model, branch), compaction markers.

**What's skipped:** tool calls, tool results, reasoning/thinking blocks, token counts, file snapshots.

## What catchup doesn't do

**No cross-agent mixing.** You pick one agent at a time. There's no unified "show me everything everywhere" view. Each agent's sessions have different shapes and mixing them would flatten meaningful differences.

**No raw replay.** If you need every tool call, result, and reasoning step for debugging, catchup is the wrong tool — grep the raw `.jsonl` or `.db` files directly.

**No writing.** catchup is read-only. It doesn't modify, delete, or tag sessions.

## Install

```bash
go install github.com/wilbeibi/catchup@latest
```

Requires Go 1.25+. Single binary, one dependency (SQLite for OpenCode, pure Go).

## License

MIT
