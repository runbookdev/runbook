#!/usr/bin/env bash
set -euo pipefail
# Usage: health-soak.sh <run_id> <expected_version>
WORKDIR="/tmp/runbook-demo-$1"
EXPECTED="$2"
FAILURES=0

for i in $(seq 1 5); do
  RUNNING=$(cat "$WORKDIR/current-version" 2>/dev/null || echo "missing")
  STATUS=$(cat  "$WORKDIR/service-status"  2>/dev/null || echo "unknown")
  if [ "$RUNNING" = "$EXPECTED" ] && [ "$STATUS" = "running" ]; then
    echo "Health check $i/5: ok (v$RUNNING, $STATUS)"
  else
    FAILURES=$((FAILURES + 1))
    echo "Health check $i/5: FAIL (version=$RUNNING status=$STATUS)"
  fi
  sleep 3
done

test "$FAILURES" -eq 0
