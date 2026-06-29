#!/usr/bin/env bash
# Build the split-screen handoff: a tmux session with two panes in the SAME
# directory (left = Claude Code, right = Codex). A background "choreographer"
# animates the left pane (work → 5-hour limit) then the right pane (catch up via
# the real catchup, continue). The script attaches in the foreground so the
# recorder (VHS) captures it. Fully deterministic — timing lives here.
set -euo pipefail

DEMO=/tmp/catchup-demo
APP="$DEMO/app"
BIN="$DEMO/bin"
SESS=demo

export TZ=UTC
export CLAUDE_CONFIG_DIR="$DEMO/.claude"
export PATH="$BIN:$PATH"
unset CLAUDE_CODE_SESSION_ID || true

# private socket: a fresh tmux server with OUR env, isolated from the user's tmux
TM="tmux -L catchup-demo"

$TM kill-server 2>/dev/null || true

COLS=$(tput cols 2>/dev/null || echo 150)
ROWS=$(tput lines 2>/dev/null || echo 32)

$TM -f "$BIN/tmux.conf" new-session -d -s "$SESS" -x "$COLS" -y "$ROWS" -c "$APP"
$TM split-window -h -t "$SESS" -c "$APP"
$TM select-pane -t "$SESS:0.0" -T ' ✷ claude  ·  ~/src/app '
$TM select-pane -t "$SESS:0.1" -T ' ⌖ codex  ·  ~/src/app '
$TM select-pane -t "$SESS:0.0"

# choreographer: wait for the recorder to settle, play left, then play right
(
  sleep 1.6
  $TM send-keys -t "$SESS:0.0" 'claude-sim' Enter
  sleep 7.8
  $TM send-keys -t "$SESS:0.1" 'codex-sim' Enter
  sleep 8.5
) &

exec $TM attach -t "$SESS"
