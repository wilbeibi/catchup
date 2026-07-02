---
name: catchup
description: Recovers prior coding-agent session context by running `catchup <agent> --since-compact`, which extracts a clean summary of a previous Codex, Claude Code, OpenCode, or Pi Agent session. Use when the user says "catch up", "what did the last session do", "get me up to speed", "I switched agents", or asks to recover/summarize a previous session before continuing. Do NOT use for the current conversation, git history, or any non-agent log.
---

# catchup

Pull a clean summary of a previous agent session into context.

You are running inside a live session, so bare `catchup` resolves to the newest session in this directory — usually *this one*. Pick the command by what the user wants:

```bash
catchup <agent> --since-compact  # another agent's latest session here
catchup --since-compact          # recover THIS session after a compaction
catchup <agent> --list           # list recent sessions
catchup <agent> -q "topic"       # search sessions
catchup <agent>/3                # read 3rd newest session
catchup <agent> --id <id>        # read exact session
```

Agents: `codex`, `claude`, `opencode`, `pi-agent`.

## Operation

- Default to `--since-compact` (final compaction segment).
- To pick up another agent's work, always name that agent — bare `catchup` finds your own session, not theirs.
- Unclear which session? Run `catchup <agent> --list` first — don't guess.
- Unclear which flag fits the request (full session, `--last N`, a specific rank/id)? Ask the user instead of guessing.
- `-q "topic"` implies `--list` — returns a listing, not a session read.
- To *continue* the same agent's session with full state, suggest the user run `catchup fork` in their terminal — don't transcript-brief when a native fork fits better. To continue in a *different* agent from the terminal, `catchup fork <agent> --into <other-agent>` starts the other agent seeded with the transcript.
- Output: Markdown, conversation only, tool calls/reasoning already stripped; `-i` for metadata only.

Run `catchup --help` for the full flag list.
