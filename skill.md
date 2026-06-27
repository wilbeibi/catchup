---
name: catchup
description: Recovers prior coding-agent session context by running the local `catchup` CLI, which extracts clean conversation summaries from Codex, Claude Code, and OpenCode history. Use when the user says "catch up", "what did the last session do", "get me up to speed", "I switched agents", or asks to recover/summarize a previous Codex/Claude/OpenCode session before continuing. Do NOT use for the current conversation, git history, or any non-agent log.
---

# catchup

Pull a clean summary of a previous agent session (yours or another tool's) into context, so handoff doesn't need copy-paste.

## Quick start

```bash
catchup claude                 # latest Claude session in this directory → Markdown
```

Run the command, read the output, then continue the user's task with that context.

## Steps

1. Pick the provider the prior work happened in: `codex`, `claude`, or `opencode`.
2. Run `catchup <provider>` and read the result. Sessions are scoped to the current working directory by default.
3. If it's the wrong session, narrow it:
   - `catchup <provider> --list` — list recent sessions to find the right one.
   - `catchup <provider> -q "<topic>"` — search by keyword.
   - `catchup <provider>/3` — the 3rd most recent session.
   - `catchup <provider> --id <session-id>` — an exact session.
4. Trim noise when a full thread is too long:
   - `catchup <provider> --last 4` — only the last 4 exchanges.
   - `catchup <provider> --since-compact` — only the final compaction segment.
   - `catchup <provider> -I` — metadata only (who/when/where/model).

## Notes

- Output is conversation only — user and assistant messages, numbered and timestamped. Tool calls and reasoning are already stripped; do not ask the user for them.
- `--last N` and `--since-compact` are mutually exclusive.
- `--json` / `--html` exist for scripts and sharing; for reading into your own context, prefer the default Markdown.
- Run `catchup <provider> --list` first whenever you're unsure which session the user means — don't guess and summarize the wrong one.
