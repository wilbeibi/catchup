# Remind me to hand off before quota runs out

This recipe turns quota data into an active reminder: at 75% used (25% left) it
suggests the matching `catchup fork` command — on the Claude Code statusline and
as a desktop notification when a Codex turn completes. For passive quota display
instead, see [quota visibility](quota-visibility.md).

## Install the helper

```sh
curl -fsSL https://raw.githubusercontent.com/wilbeibi/catchup/main/recipes/quota-reminder.py \
  -o ~/.local/bin/quota-reminder.py
```

Python 3 only, no other dependencies. The script runs on existing agent events
(statusline refresh, turn completion) — no polling, no background process.

## Claude Code

In `~/.claude/settings.json`, keep your existing statusline command and append
the reminder. Minimal example:

```json
"statusLine": {
  "type": "command",
  "command": "input=$(cat); base=$(printf '%s' \"$input\" | jq -r '.model.display_name'); rem=$(printf '%s' \"$input\" | python3 \"$HOME/.local/bin/quota-reminder.py\" claude); printf '%s%s' \"$base\" \"${rem:+ · $rem}\""
}
```

Replace the `base` part with your current statusline, reading the captured
`$input` instead of stdin. Over 75% used, `rem` appends something like
`5h: 18% left · consider: catchup fork claude --into codex`.

For an additional one-shot desktop notification, prefix the script call with
`CATCHUP_QUOTA_DESKTOP=1`. Printed statusline text repeats by nature; the
desktop notification fires once per session and window.

## Codex CLI

Add to `~/.codex/config.toml` (user-level; Codex ignores `notify` in
project-local config):

```toml
notify = ["python3", "/home/YOU/.local/bin/quota-reminder.py", "codex"]
```

Codex passes the turn-complete event as a final JSON argument; the script reads
the session log's tail for quota and sends one desktop notification, for
example `5h: 10% left · consider: catchup fork codex --into claude`.
Notifications use `notify-send` (Linux); override with a single executable
receiving `title body` via `CATCHUP_QUOTA_NOTIFY`. On macOS, point it at a
small script calling `osascript`.

## How it behaves

- Threshold: 75% used across active windows (5h and weekly; spend limits count
  above 100 as exceeded). Windows that already reset are ignored.
- Once per session and window: state under
  `$XDG_STATE_HOME/catchup-quota/` (default `~/.local/state/`) stores only
  window reset timestamps — no transcript text, and nothing leaves the machine.
- Failures are silent: this is optional UI glue and must never disrupt the
  agent. Missing quota data means no reminder that turn.

## Test it

Claude Code, deterministic:

```sh
echo '{"session_id":"t","rate_limits":{"five_hour":{"used_percentage":80,"resets_at":9999999999}}}' \
  | python3 ~/.local/bin/quota-reminder.py claude
```

Expect `5h: 20% left · consider: catchup fork claude --into codex`.

Codex: try the newest session (silence is correct below 75%):

```sh
id=$(ls -t ~/.codex/sessions/*/*/*/rollout-*.jsonl | head -1 | sed -E 's/.*-([0-9a-f-]{36})\.jsonl/\1/')
CATCHUP_QUOTA_NOTIFY='logger -t catchup' python3 ~/.local/bin/quota-reminder.py codex \
  "{\"type\":\"agent-turn-complete\",\"thread-id\":\"$id\"}"
```

Then `journalctl -t catchup` (or your logger) for the captured notification.

For agents assisting with setup: this recipe is opt-in; change settings only
when requested, preserve existing statusline fields, and never install the
`notify` hook or edit `config.toml` without explicit approval.
