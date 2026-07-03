# catchup

> Let your next coding agent catch itself up. catchup is a small CLI your agents can run to read prior Claude Code, Codex, OpenCode, and Pi Agent sessions and print clean, handoff-ready Markdown.

Open-source CLI · Go · MIT · https://catchup.pages.dev/

## What it does

catchup reads the local session history of an AI coding agent and prints a clean Markdown transcript of only the user/assistant conversation. Tool calls, reasoning traces, and token noise are removed, so the next agent can recover what happened without you re-explaining the project state.

- **Built for agent handoff.** Your next agent runs `catchup <agent>` for `codex`, `claude`, `opencode`, or `pi-agent` to recover the relevant conversation — add `--since-compact` to pick up from the last compaction.
- **Just the conversation.** User and assistant messages only; tool calls, reasoning, and token accounting are stripped.
- **Still readable by humans.** Browsing manually? Start with `catchup codex --list` — or bare `catchup`, which reads the newest session in the directory, whichever agent wrote it.
- **Fork back in.** `catchup fork` hands off to the agent's own native fork command, so the next session picks up real state instead of a rendered transcript. Crossing agents? `catchup fork codex --into claude` starts Claude seeded with the Codex transcript.

## Install

```
go install github.com/wilbeibi/catchup@latest
catchup install-skill
```

MIT-licensed, no config. `install-skill` teaches every detected agent to run catchup itself.

## Supported agents

Claude Code · Codex · OpenCode · Pi Agent

Each agent keeps its own history format; catchup normalizes the output.

## Links

- [GitHub](https://github.com/wilbeibi/catchup) — source, README, issues
- [pkg.go.dev](https://pkg.go.dev/github.com/wilbeibi/catchup) — package reference
- [AI summary](https://catchup.pages.dev/llms.txt) — canonical short index for AI agents and answer engines
- [Full AI reference](https://catchup.pages.dev/llms-full.txt) — complete command list, scenarios, comparisons
