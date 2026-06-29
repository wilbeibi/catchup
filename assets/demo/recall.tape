# Scenario 2: recall. Inside a project dir you launch `opencode` (no args); in the
# interactive session you ask about rate limits, and opencode uses the real
# `catchup` to find and recall the past Claude session that solved it.
# Regenerate with: bash assets/demo/render.sh  (run from the repo root).

Output assets/recall.gif

Set Shell "bash"
# High pixel density at the SAME column/row layout (FontSize + size scaled together)
# so it stays crisp when shown small in the README, without looking oversized.
Set FontFamily "Hack"
Set FontSize 21
Set Width 1680
Set Height 548
Set Padding 30
Set Theme "Catppuccin Mocha"

Hide
Type "export CLAUDE_CONFIG_DIR=/tmp/catchup-demo/.claude" Enter
Type "export TZ=UTC" Enter
Type "export PATH=/tmp/catchup-demo/bin:$PATH" Enter
Type "unset CLAUDE_CODE_SESSION_ID" Enter
Type "export PS1='~/src/app $ '" Enter
Type "cd /tmp/catchup-demo/app" Enter
Type "clear" Enter
Show

Sleep 0.8
Type "opencode"
Sleep 400ms
Enter
Sleep 10
