# catchup

> Switch coding agents without re-explaining. catchup reads your Claude Code, Codex, OpenCode, and Pi Agent session history and prints clean, handoff-ready Markdown — just the conversation, none of the tool-call noise.

Open-source CLI · Go · MIT · https://catchup.pages.dev/

## What it does

catchup reads the local session history of an AI coding agent and prints a clean Markdown transcript of only the user/assistant conversation. Tool calls, reasoning traces, and token noise are removed, so the next agent — or you, days later — can pick up the thread without being re-briefed.

- **One command, any agent.** `catchup claude`, `catchup codex`, `catchup opencode`, `catchup pi-agent` produce the same clean output, whichever tool wrote the history.
- **Just the conversation.** User and assistant messages only; tool calls, reasoning, and token accounting are stripped.
- **Built for handoff.** Pipe the output to the next agent, or read it yourself later — no re-explaining, no copy-pasting context between windows.

## Install

```
go install github.com/wilbeibi/catchup@latest
```

MIT-licensed, no config.

## Supported agents

- Claude Code — `catchup claude`
- Codex — `catchup codex`
- OpenCode — `catchup opencode`
- Pi Agent — `catchup pi-agent`

Read-only: no cross-agent mixing, no raw replay, no writing. catchup never touches your sessions.

## Links

- [GitHub](https://github.com/wilbeibi/catchup) — source, README, issues
- [pkg.go.dev](https://pkg.go.dev/github.com/wilbeibi/catchup) — package reference
- [Full AI reference](https://catchup.pages.dev/llms-full.txt) — complete command list, scenarios, comparisons
