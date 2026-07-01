# catchup

> Let your next coding agent catch itself up. catchup is a small CLI your agents can run to read prior Claude Code, Codex, OpenCode, and Pi Agent sessions and print clean, handoff-ready Markdown.

Open-source CLI · Go · MIT · https://catchup.pages.dev/

## What it does

catchup reads the local session history of an AI coding agent and prints a clean Markdown transcript of only the user/assistant conversation. Tool calls, reasoning traces, and token noise are removed, so the next agent can recover what happened without you re-explaining the project state.

- **Built for agent handoff.** Your next agent runs `catchup claude`, `catchup codex`, `catchup opencode`, or `catchup pi-agent` to recover the relevant conversation.
- **Just the conversation.** User and assistant messages only; tool calls, reasoning, and token accounting are stripped.
- **Still readable by humans.** Browsing manually? Start with `catchup codex --list`, then open the exact session you want.
- **Fork back in.** `catchup fork` hands off to the agent's own native fork command, so the next session picks up real state instead of a rendered transcript.

## Install

```
go install github.com/wilbeibi/catchup@latest
```

MIT-licensed, no config.

## Supported agents

- Claude Code — `catchup claude --list`
- Codex — `catchup codex --list`
- OpenCode — `catchup opencode --list`
- Pi Agent — `catchup pi-agent --list`

No cross-agent mixing, no raw replay: each agent's history stays separate and unabridged.

## Links

- [GitHub](https://github.com/wilbeibi/catchup) — source, README, issues
- [pkg.go.dev](https://pkg.go.dev/github.com/wilbeibi/catchup) — package reference
- [Full AI reference](https://catchup.pages.dev/llms-full.txt) — complete command list, scenarios, comparisons
