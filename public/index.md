# catchup

> Let your next coding agent catch itself up. catchup is a small CLI your agents can run to read prior Claude Code, Codex, Cursor, Cline, Kimi, Antigravity, OpenCode, and Pi Agent sessions and print clean, handoff-ready Markdown.

Open-source CLI · Go · MIT · https://catchup.pages.dev/

## What it does

catchup reads the local session history of an AI coding agent and prints a clean Markdown transcript of only the user/assistant conversation. Tool calls, reasoning traces, and token noise are removed, so the next agent can recover what happened without you re-explaining the project state.

Every command is one of three jobs with a session:

- **Recap.** Pull a past session back into context. `catchup <agent> --since-compact` for `claude`, `codex`, `cursor`, `cline`, `kimi`, `agy`, `opencode`, or `pi-agent` reads the tail after the last compaction; drop the flag for the whole thing.
- **Find.** Locate the right session first. `catchup <agent> --list` lists what ran here, `-q "keyword"` searches by keyword, and `catchup <agent>/N` or `--id <id>` opens an exact one.
- **Hand off.** Continue the work. `catchup fork <agent>` resumes through the agent's own native fork command with real state; crossing agents, `catchup fork codex --into claude` starts Claude seeded with the Codex transcript.

The output is just the conversation: user and assistant messages only, with tool calls, reasoning, and token accounting stripped. Browsing manually? Bare `catchup` reads the newest session in the directory, whichever agent wrote it.

## Install

```
# Homebrew
brew install wilbeibi/tap/catchup

# or prebuilt binary
curl -fsSL https://raw.githubusercontent.com/wilbeibi/catchup/main/scripts/install.sh | sh

# or with Go
go install github.com/wilbeibi/catchup@latest

catchup install-skill
```

MIT-licensed, no config. `install-skill` teaches every detected agent to run catchup itself.

## Supported agents

Claude Code · Codex · Cursor · Cline · Kimi · Antigravity (agy) · OpenCode · Pi Agent

Each agent keeps its own history format; catchup normalizes the output.

## Links

- [GitHub](https://github.com/wilbeibi/catchup) — source, README, issues
- [pkg.go.dev](https://pkg.go.dev/github.com/wilbeibi/catchup) — package reference
- [AI summary](https://catchup.pages.dev/llms.txt) — canonical short index for AI agents and answer engines
- [Full AI reference](https://catchup.pages.dev/llms-full.txt) — complete command list, scenarios, comparisons
