# catchup

> Let your next coding agent catch itself up. catchup is a small CLI your agents can run to read prior Claude Code, Codex, Copilot CLI, Cursor, Cline, Kimi, Antigravity, OpenCode, Pi Agent, ZCode, and DeepSeek Harness sessions and print clean, handoff-ready Markdown.

Open-source CLI · Go · https://catchup.pages.dev/

## Install

```
curl -fsSL https://catchup.pages.dev/install | sh
```

Homebrew: `brew install wilbeibi/tap/catchup`

Windows: [download the release ZIP](https://github.com/wilbeibi/catchup/releases)

## What it does

catchup reads the local session history of an AI coding agent. Human-facing Markdown contains the user/assistant conversation without tool activity, reasoning traces, or token noise. Agent handoffs also retain tool calls the source log marked failed, so the next agent sees known dead ends.

Every command is one of three jobs with a session:

- **Recap.** Pull a past session back into context. `catchup <agent> --since-compact` for `claude`, `codex`, `copilot`, `cursor`, `cline`, `kimi`, `agy`, `opencode`, `pi-agent`, `zcode`, or `deepseek` reads the tail after the last compaction; drop the flag for the whole thing.
- **Find.** Locate the right session first. `catchup <agent> --list` lists what ran here, `-q "keyword"` searches by keyword, and `catchup <agent>/N` or `--id <id>` opens an exact one.
- **Hand off.** Continue the work. `catchup fork <agent>` resumes through the agent's own native fork command with real state; crossing agents, `catchup fork codex --into claude` starts Claude with the Codex handoff transcript.

The default output is the conversation. `--agent` adds source-marked failed tool calls for an agent that is picking up the work. Bare `catchup` reads the newest session in the directory, whichever agent wrote it.

## Supported agents

Claude Code · Codex · Copilot CLI · Cursor · Cline · Kimi · Antigravity (agy) · OpenCode · Pi Agent · ZCode · DeepSeek Harness (dsh)

Each agent keeps its own history format; catchup normalizes the output.

## Common tasks

- [Continue a Claude Code session in Codex](https://catchup.pages.dev/handoff/claude-to-codex/) — `catchup fork claude --into codex`
- [Keep working after an agent hits its usage limit](https://catchup.pages.dev/usage-limit/) — move the conversation to an agent that still has quota
- [Find and reopen a past session](https://catchup.pages.dev/find-session/) — `catchup --list`, `-q "keyword"`, `--id`
- [Native resume vs. HANDOFF.md vs. cross-agent handoff](https://catchup.pages.dev/compare/) — what each keeps and loses

## Links

- [GitHub](https://github.com/wilbeibi/catchup) — source, README, issues
- [pkg.go.dev](https://pkg.go.dev/github.com/wilbeibi/catchup) — package reference
- [AI summary](https://catchup.pages.dev/llms.txt) — canonical short index for AI agents and answer engines
- [Full AI reference](https://catchup.pages.dev/llms-full.txt) — complete command list, scenarios, comparisons
