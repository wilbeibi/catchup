#!/usr/bin/env bash
# Print aggregate installer metrics from the Pages Functions Analytics Engine
# dataset. Requires a Cloudflare token with Account Analytics: Read.
set -euo pipefail

days=${1:-30}
if ! [[ $days =~ ^[1-9][0-9]*$ ]]; then
  echo "usage: $0 [positive-number-of-days]" >&2
  exit 2
fi

: "${CLOUDFLARE_API_TOKEN:?set CLOUDFLARE_API_TOKEN}"

account_id=${CLOUDFLARE_ACCOUNT_ID:-}
if [[ -z $account_id ]]; then
  accounts=$(curl -fsS -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
    https://api.cloudflare.com/client/v4/accounts)
  account_id=$(jq -r '.result | if length == 1 then .[0].id else empty end' <<<"$accounts")
fi
if [[ -z $account_id ]]; then
  echo "set CLOUDFLARE_ACCOUNT_ID when this token can access multiple accounts" >&2
  exit 2
fi

query="SELECT index1 AS event, SUM(_sample_interval * double1) AS count
FROM catchup_events
WHERE timestamp >= now() - INTERVAL '$days' DAY
GROUP BY event
ORDER BY event"

curl -fsS -H "Authorization: Bearer $CLOUDFLARE_API_TOKEN" \
  --data "$query" \
  "https://api.cloudflare.com/client/v4/accounts/$account_id/analytics_engine/sql" |
  jq -r '.data[]? | [.event, .count] | @tsv'
