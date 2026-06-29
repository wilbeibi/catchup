# Scenario 1: split-screen handoff. Claude Code hits its 5-hour limit (left pane);
# Codex, in the same directory, catches up by running the real `catchup` and
# continues (right pane). The whole split is choreographed by handoff-split.sh;
# this tape just launches it and records.
# Regenerate with: bash assets/demo/render.sh  (run from the repo root).

Output assets/handoff.gif

Set Shell "bash"
# High pixel density at the SAME column/row layout (FontSize + size scaled together)
# so it stays crisp when shown small in the README, without looking oversized.
Set FontFamily "Hack"
Set FontSize 21
Set Width 1780
Set Height 615
Set Padding 26
Set Theme "Catppuccin Mocha"

Hide
Type "/tmp/catchup-demo/bin/handoff-split.sh" Enter
Sleep 1.2
Show
Sleep 17
