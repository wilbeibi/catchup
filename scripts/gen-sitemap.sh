#!/usr/bin/env bash
# Regenerate public/sitemap.xml with lastmod taken from git, not hand-edited.
#
# The previous sitemap carried a hardcoded lastmod that went stale the moment
# anything shipped, which tells a crawler the opposite of the truth. Each URL's
# lastmod here is the commit date of the file that backs it.
#
# Run from the site branch root. Requires full git history (actions/checkout
# with fetch-depth: 0) — a shallow clone has no commit dates for most files.
set -euo pipefail

cd "$(dirname "$0")/.."
BASE="https://catchup.pages.dev"
OUT="public/sitemap.xml"

# Commit date (YYYY-MM-DD) of the file's last change; today if git has no
# record of it (new, uncommitted, or shallow clone).
lastmod() {
  local d
  d="$(git log -1 --format=%cs -- "$1" 2>/dev/null || true)"
  [ -n "$d" ] && printf '%s' "$d" || date -u +%F
}

emit() { # path  loc  priority  changefreq
  printf '  <url>\n    <loc>%s</loc>\n    <lastmod>%s</lastmod>\n    <changefreq>%s</changefreq>\n    <priority>%s</priority>\n  </url>\n' \
    "$2" "$(lastmod "$1")" "$4" "$3"
}

{
  printf '<?xml version="1.0" encoding="UTF-8"?>\n'
  printf '<urlset xmlns="http://www.sitemaps.org/schemas/sitemap/0.9">\n'

  emit public/index.html    "$BASE/"              1.0 monthly
  emit public/llms.txt      "$BASE/llms.txt"      0.9 monthly
  emit public/index.md      "$BASE/index.md"      0.8 monthly
  emit public/llms-full.txt "$BASE/llms-full.txt" 0.8 monthly

  # Every answer page: public/<path>/index.html -> $BASE/<path>/
  while IFS= read -r f; do
    emit "$f" "$BASE/${f#public/}" 0.8 monthly
  done < <(find public -mindepth 2 -name index.html | sed 's|index.html$||' | sort)

  printf '</urlset>\n'
} > "$OUT"

printf 'wrote %s (%s urls)\n' "$OUT" "$(grep -c '<loc>' "$OUT")"
