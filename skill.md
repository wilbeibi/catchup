---
name: catchup
description: Recovers prior coding-agent session context by running `catchup <provider>`, which extracts clean conversation summaries from Codex, Claude Code, OpenCode, and Pi Agent history. Use when the user says "catch up", "what did the last session do", "get me up to speed", "I switched agents", or asks to recover/summarize a previous Codex/Claude/OpenCode/Pi Agent session before continuing. Do NOT use for the current conversation, git history, or any non-agent log.
---

# catchup

Pull a clean summary of a previous agent session into context.

```bash
catchup <provider>              # latest → Markdown
catchup <provider> --list       # browse recent sessions
catchup <provider> -q "topic"   # search by keyword
catchup <provider>/3            # 3rd most recent
catchup <provider> --id <id>    # exact session
```

Providers: `codex`, `claude`, `opencode`, `pi-agent`.

## Operation

Always run `--list` first when unsure which session the user means — don't guess.

- `--last N` — last N exchanges only.
- `--since-compact` — final compaction segment only. Mutually exclusive with `--last`.

## Output

- Conversation only: user/assistant messages, numbered and timestamped. Tool calls and reasoning are already stripped.
- Default Markdown; `--json` / `--html` for scripts and sharing.
- `-I` for metadata only (who/when/where/model).
