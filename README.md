<div align="center">

# baton

### Handoff-ready summaries from your agent sessions

**Single binary · 100% local · Works offline**

</div>

## The problem

You use multiple coding agents — Codex, Claude Code, OpenCode. Each keeps its conversation history in a different place, in a different format. When you need to hand off a session's context to another agent, you open the old UI, scroll through walls of tool calls and reasoning blocks, find the actual messages, copy a chunk, and paste.

baton reads all three local histories and gives you a clean, compact summary: just the conversation, stripped of noise.

## What it does

```bash
baton codex        # latest Codex session → clean Markdown to stdout
baton claude       # latest Claude session
baton opencode     # latest OpenCode session
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
## 1. user · 2026-06-26 14:25
Refactor the auth middleware to use the new token format

## 2. assistant · 2026-06-26 14:26  
Here's the updated middleware...
```

That's it. You can pipe it, save it, or hand it to another agent.

## Finding sessions

```bash
baton codex --list                    # list recent sessions
baton codex -q "auth"                 # search for sessions about auth
baton codex/3                         # 3rd most recent session
baton claude --id <exact-session-id>  # exact session (for scripts)
```

## Other output formats

```bash
baton codex -I      # metadata only (who/when/where, no conversation)
baton codex --last 4 # last 4 exchanges only (each = your prompt + the reply)
baton codex --html   # self-contained HTML (for sharing in a browser)
baton codex --json   # structured JSON (for scripts)
```

## Supported agents

baton reads sessions from Codex, Claude Code, and OpenCode. It finds them automatically under `~/.codex`, `~/.claude`, and `~/.local/share/opencode`. Each can be overridden with an environment variable: `CODEX_HOME`, `CLAUDE_CONFIG_DIR`, `XDG_DATA_HOME`.

**What's included:** user and assistant messages, session metadata (title, directory, model, branch), compaction markers.

**What's skipped:** tool calls, tool results, reasoning/thinking blocks, token counts, file snapshots.

## What baton doesn't do

**No cross-agent mixing.** You pick one agent at a time. There's no unified "show me everything everywhere" view. Each agent's sessions have different shapes and mixing them would flatten meaningful differences.

**No raw replay.** If you need every tool call, result, and reasoning step for debugging, baton is the wrong tool — grep the raw `.jsonl` or `.db` files directly.

**No writing.** baton is read-only. It doesn't modify, delete, or tag sessions.

## Install

```bash
go install github.com/wilbeibi/baton@latest
```

Requires Go 1.25+. Single binary, one dependency (SQLite for OpenCode, pure Go).

## License

MIT
