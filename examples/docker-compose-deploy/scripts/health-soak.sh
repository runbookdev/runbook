#!/usr/bin/env bash
set -euo pipefail
# Usage: health-soak.sh <app_url>
APP_URL="$1"
FAILURES=0

for i in $(seq 1 6); do
  STATUS=$(curl -sf --max-time 5 "${APP_URL}/health" | jq -r '.status' 2>/dev/null || echo "unreachable")
  echo "Health poll $i/6: $STATUS"
  if [ "$STATUS" != "ok" ]; then
    FAILURES=$((FAILURES + 1))
    echo "WARNING: unhealthy response on poll $i"
  fi
  sleep 10
done

test "$FAILURES" -eq 0
