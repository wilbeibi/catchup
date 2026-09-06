# See quota limits before a handoff

Enable quota visibility in your coding agent's statusline to help choose when to switch,
or have Codex send a turn-end reminder. This setup is optional. Catchup can recover an
existing transcript after you reach a limit.

## Claude Code

Run this inside Claude Code:

```text
/statusline Preserve my existing fields. Add five-hour and weekly quota usage and reset times when available. At 80% used, highlight the quota and show "Consider handoff: catchup fork claude --into codex". Omit unavailable quota fields.
```

Review the generated script and settings changes before accepting them.
Claude supplies quota data to the script and updates the display as messages arrive.
Availability depends on your account. Missing data does not mean zero usage.

The 80% threshold is a suggested early cue, not an estimate of remaining turns.
This prompt requests a custom warning. Verify the generated script implements it.

See [Claude Code statusline documentation](https://code.claude.com/docs/en/statusline) for available fields and setup details.

## Codex CLI

1. Run `/statusline` inside Codex CLI.
2. Enable the available rate-limit items while keeping your existing fields.
3. Confirm the selection to save it.

The footer displays quota information as you work. Context-window usage measures something different from account quota.
Use roughly 20% remaining as a cue to consider switching, especially before a long task.
This uses Codex's native display. It does not add a custom threshold alert.

See [Codex statusline documentation](https://learn.chatgpt.com/docs/developer-commands?surface=cli#configure-footer-items-with-statusline) for the picker and supported items.

## Codex CLI: optional turn-end reminder

Codex's native warning text cannot be customized, but its `notify` hook runs when a
turn completes. Save the helper and point `notify` at it:

```sh
curl -fsSL https://raw.githubusercontent.com/wilbeibi/catchup/main/recipes/quota-reminder.py \
  -o ~/.local/bin/quota-reminder.py
```

In `~/.codex/config.toml` (user-level; Codex ignores `notify` in project-local configs):

```toml
notify = ["python3", "/home/YOU/.local/bin/quota-reminder.py"]
```

Restart Codex. When the session's newest quota snapshot shows 25% or less left,
the helper sends one desktop notification per quota window, for example
`5h: 2% left · consider: catchup fork codex --into claude`. Marker files under
`~/.local/state/catchup-quota/` dedupe per window — delete them to re-test.
The notifier is `notify-send`; on macOS, edit the script's last line. Failures stay
silent and never disrupt the agent.

To try it against your newest session (silence is correct below 75% used):

```sh
id=$(ls -t ~/.codex/sessions/*/*/*/rollout-*.jsonl | head -1 | sed -E 's/.*-([0-9a-f-]{36})\.jsonl/\1/')
python3 ~/.local/bin/quota-reminder.py "{\"type\":\"agent-turn-complete\",\"thread-id\":\"$id\"}"
```

## Switch when ready

Run the appropriate command in a terminal, from your project directory:

```sh
catchup fork claude --into codex
catchup fork codex --into claude
```

If you want a smaller handoff, compact in the source agent while it is still available.
Then append `--since-compact` to the fork command. Compaction is optional.

For agents assisting with setup: read this recipe only when quota guidance is needed.
Change settings only when requested, preserve existing fields, and avoid repeating the suggestion within the conversation.
