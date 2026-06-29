#!/usr/bin/env bash
# Seed synthetic Claude sessions + demo scripts for the README GIFs.
#
# Everything lives under /tmp/catchup-demo so nothing touches real ~/.claude.
# Fixtures' `cwd` is stamped to $APP because catchup filters --list / -q / rank
# lookups by the process working directory (internal/cli/cli.go,
# internal/claude/claude.go). The tapes / split-screen run catchup from $APP.
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

DEMO=/tmp/catchup-demo
APP="$DEMO/app"                 # the shared project directory both agents sit in
CLAUDE_DIR="$DEMO/.claude"
PROJ="$CLAUDE_DIR/projects/app"
BIN="$DEMO/bin"

export TZ=UTC                   # touch -t and catchup timestamps render in UTC

# 1. build catchup
( cd "$REPO_ROOT" && go build -o catchup . )

# 2. fresh demo tree
rm -rf "$DEMO"
mkdir -p "$APP" "$PROJ" "$BIN"

# 3. real binary + demo scripts on a private PATH
cp "$REPO_ROOT/catchup" "$BIN/catchup"
cp "$SCRIPT_DIR/agents/opencode" \
   "$SCRIPT_DIR/agents/claude-sim" \
   "$SCRIPT_DIR/agents/codex-sim" \
   "$SCRIPT_DIR/handoff-split.sh" \
   "$BIN/"
cp "$SCRIPT_DIR/tmux.conf" "$BIN/tmux.conf"
chmod +x "$BIN/catchup" "$BIN/opencode" "$BIN/claude-sim" "$BIN/codex-sim" "$BIN/handoff-split.sh"

# rc for the clean bash that runs inside each tmux pane (see tmux.conf)
cat > "$BIN/panerc" <<RC
export PATH="$BIN:\$PATH"
export CLAUDE_CONFIG_DIR="$CLAUDE_DIR"
export TZ=UTC
unset CLAUDE_CODE_SESSION_ID
PS1='\$ '
RC

# 4. fixtures: one JSON object per line, conforming to internal/claude/claude.go.
#    cwd MUST equal $APP; only the two rate-limit sessions contain "rate limit"
#    so `-q "rate limit"` matches exactly those.

cat > "$PROJ/a1b2c3d4.jsonl" <<JSONL
{"type":"user","sessionId":"a1b2c3d4","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-26T15:01:00Z","aiTitle":"fix flaky auth test","message":{"role":"user","content":"The auth test fails about 1 in 5 runs in CI. Can you find the flake?"}}
{"type":"assistant","sessionId":"a1b2c3d4","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-26T15:02:00Z","message":{"role":"assistant","content":[{"type":"text","text":"It's a race in token refresh — two goroutines refresh at once and one overwrites the other's token. I'll guard refreshToken() with a mutex."}]}}
{"type":"user","sessionId":"a1b2c3d4","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-26T15:02:30Z","isCompactSummary":true,"message":{"role":"user","content":"Context limit reached. Recap: flaky auth test traced to a token-refresh race in refreshToken(); applying a mutex was in progress."}}
JSONL

cat > "$PROJ/e5f6a7b8.jsonl" <<JSONL
{"type":"user","sessionId":"e5f6a7b8","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-25T19:38:00Z","aiTitle":"rate-limit backoff","message":{"role":"user","content":"Our API returns 429s under load — how should the client handle the rate limit and back off?"}}
{"type":"assistant","sessionId":"e5f6a7b8","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-25T19:40:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Use exponential backoff with full jitter, cap the delay at 30s, and honor the Retry-After header when the server sends it."}]}}
JSONL

cat > "$PROJ/c3d4e5f6.jsonl" <<JSONL
{"type":"user","sessionId":"c3d4e5f6","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-21T11:10:00Z","aiTitle":"429 retries + jitter","message":{"role":"user","content":"What is a good retry policy for rate limit errors from a flaky upstream?"}}
{"type":"assistant","sessionId":"c3d4e5f6","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-21T11:12:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Retry only idempotent calls, add jitter so clients don't synchronize, and give up after a bounded number of attempts."}]}}
JSONL

cat > "$PROJ/d4e5f6a7.jsonl" <<JSONL
{"type":"user","sessionId":"d4e5f6a7","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-19T10:00:00Z","aiTitle":"refactor config loader","message":{"role":"user","content":"Can you split the giant config loader into per-section parsers?"}}
{"type":"assistant","sessionId":"d4e5f6a7","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-19T10:03:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Yes — I'll extract loadServer, loadDB, and loadAuth and keep a thin top-level Load that composes them."}]}}
JSONL

cat > "$PROJ/f6a7b8c9.jsonl" <<JSONL
{"type":"user","sessionId":"f6a7b8c9","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-15T10:00:00Z","aiTitle":"add /healthz endpoint","message":{"role":"user","content":"Add a health check endpoint that reports DB connectivity."}}
{"type":"assistant","sessionId":"f6a7b8c9","cwd":"$APP","gitBranch":"main","timestamp":"2026-06-15T10:04:00Z","message":{"role":"assistant","content":[{"type":"text","text":"Added GET /healthz; it pings the DB with a 2s timeout and returns 200 ok or 503 with the error."}]}}
JSONL

# 5. file mtimes drive the UPDATED column and the `updated:` field (newest first)
touch -t 202606261502.00 "$PROJ/a1b2c3d4.jsonl"
touch -t 202606251940.00 "$PROJ/e5f6a7b8.jsonl"
touch -t 202606211112.00 "$PROJ/c3d4e5f6.jsonl"
touch -t 202606191003.00 "$PROJ/d4e5f6a7.jsonl"
touch -t 202606151004.00 "$PROJ/f6a7b8c9.jsonl"

echo "--- seeded sessions ---"
( cd "$APP" && CLAUDE_CONFIG_DIR="$CLAUDE_DIR" "$BIN/catchup" claude --list )
