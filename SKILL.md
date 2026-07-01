---
name: catchup
description: Recovers prior coding-agent session context by running `catchup <agent> --since-compact`, which extracts a clean summary of a previous Codex, Claude Code, OpenCode, or Pi Agent session. Use when the user says "catch up", "what did the last session do", "get me up to speed", "I switched agents", or asks to recover/summarize a previous session before continuing. Do NOT use for the current conversation, git history, or any non-agent log.
---

# catchup

```bash
catchup <agent> --since-compact
```

Agents: `codex`, `claude`, `opencode`, `pi-agent`.

## Operation

- Default to `--since-compact` (final compaction segment).
- Unclear which session? Run `catchup <agent> --list` first — don't guess.
- Unclear which flag fits the request (full session, `--last N`, a specific rank/id)? Ask the user instead of guessing.
- `-q "topic"` implies `--list` — returns a listing, not a session read.
- Output: Markdown, conversation only, tool calls/reasoning already stripped.

Run `catchup --help` for the full flag list.
