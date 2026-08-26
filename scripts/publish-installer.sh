#!/usr/bin/env bash
# Copy the installer from main into public/ so it is served from the site's own
# domain: `curl -fsSL https://catchup.pages.dev/install.sh | sh`.
#
# The point of hosting it here rather than redirecting to GitHub is reach —
# raw.githubusercontent.com is blocked on some corporate networks and from
# China, which is the last domain you want in a first-run command.
#
# scripts/install.sh on main stays the only copy anyone edits. This runs at
# deploy time and the result is gitignored, so the site branch never holds a
# second version that can drift. A push to install.sh on main dispatches this
# deploy (see .github/workflows/republish-installer.yml on main).
set -euo pipefail

cd "$(dirname "$0")/.."
# No --depth: a shallow fetch would mark this clone shallow, and
# gen-sitemap.sh needs the full history it was checked out with.
git fetch --quiet origin main
git show origin/main:scripts/install.sh > public/install.sh
printf 'published public/install.sh from %s\n' "$(git rev-parse --short origin/main)"
