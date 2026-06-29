#!/usr/bin/env bash
# Regenerate the README demo GIFs: seed synthetic sessions, then run both tapes.
# Requires vhs (https://github.com/charmbracelet/vhs).
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"
cd "$REPO_ROOT"

bash assets/demo/seed.sh
vhs assets/demo/handoff.tape
tmux -L catchup-demo kill-server 2>/dev/null || true   # clean up the split-screen server
vhs assets/demo/recall.tape

echo "Done → assets/handoff.gif, assets/recall.gif"
