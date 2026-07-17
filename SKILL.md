---
name: catchup
description: Recovers prior coding-agent session context by running `catchup <agent> --since-compact`, which extracts a clean summary of a previous Codex, Claude Code, Antigravity, Kimi, OpenCode, or Pi Agent session. Use when the user says "catch up", "what did the last session do", "get me up to speed", "I switched agents", or asks to recover/summarize a previous session before continuing. Do NOT use for the current conversation, git history, or any non-agent log.
---

# catchup

You are running inside a live session, so bare `catchup` resolves to the newest session in this directory — usually *this one*. Every command is one of three jobs — recap a session, find the right one, or hand it off:

```bash
# RECAP — pull a session into context (how much; default is the whole thing)
catchup <agent> --since-compact   # another agent's latest, tail after its last compaction
catchup --since-compact           # recover THIS session after a compaction
catchup <agent> --last 20         # just the last 20 exchanges

# FIND — locate the right session first (which one; default is newest here)
catchup <agent> --list            # recent sessions here
catchup <agent> -q "topic"        # search by keyword
catchup <agent>/3                 # the 3rd newest
catchup <agent> --id <id>         # an exact session id

# HAND OFF — continue the work (suggest the user run these in a terminal)
catchup fork <agent>                    # native resume, full state
catchup fork <agent> --into <other>     # seed a different agent with the transcript
```

Agents: `codex`, `claude`, `agy` (Antigravity), `kimi`, `opencode`, `pi-agent`.

## Operation

- Default to `--since-compact` (final compaction segment).
- To pick up another agent's work, always name that agent — bare `catchup` finds your own session, not theirs.
- Unclear which session? Run `catchup <agent> --list` first — don't guess.
- Unclear which flag fits the request (full session, `--last N`, a specific rank/id)? Ask the user instead of guessing.
- `-q "topic"` implies `--list` — returns a listing, not a session read.
- Sessions are keyed to the directory where they ran. In a fresh git worktree, a moved repo, or a re-clone, add `--dir <original path>` to select them — e.g. continue work in an isolated tree with `git worktree add ../fix && cd ../fix && catchup fork claude --dir <original dir>`. `--dir` is local-only; for another machine, run catchup there over ssh.
- To *continue* the same agent's session with full state, suggest the user run `catchup fork` in their terminal — don't transcript-brief when a native fork fits better. To continue in a *different* agent from the terminal, `catchup fork <agent> --into <other-agent>` starts the other agent seeded with the transcript. Add `--model <name>` (the launched agent's own model name) when the user wants a specific model.
- To continue from a transcript that is *not* in a local session store — a `handoff.md` someone sent, a URL, or a pipe — `catchup fork --into <agent> --from <file | - | http(s) url>`. Any text document seeds (a transcript or hand-written handoff notes); same-agent `--into` is fine here. There is no flag for the transport: whatever delivered the bytes (scp, Taildrop, mail, `aws s3 cp … -`) just pipes into `--from -` or lands as the file.
- Output: Markdown, conversation only, tool calls/reasoning already stripped; `-i` for metadata only.
- Moving a session somewhere else? stdout is the wire format: pipe it (`catchup claude | rg -C3 "topic"`), save it (`catchup codex > handoff.md`) and send the file by any means, or read another machine directly (`ssh box catchup codex --last 20`). No flag needed — the pipe is the transport, and `fork --into <agent> --from -` is its receiving end.

Run `catchup --help` for the full flag list.
