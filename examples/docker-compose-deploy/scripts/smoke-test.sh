#!/usr/bin/env bash
set -euo pipefail
# Usage: smoke-test.sh <app_url> <expected_version>
APP_URL="$1"
EXPECTED_VERSION="$2"

# Version check
RUNNING=$(curl -sf "${APP_URL}/version" | jq -r '.version')
echo "Running version: $RUNNING"
test "$RUNNING" = "$EXPECTED_VERSION"

# Key endpoint checks
for ENDPOINT in /health /ready /metrics; do
  CODE=$(curl -o /dev/null -sw '%{http_code}' "${APP_URL}${ENDPOINT}")
  echo "GET ${APP_URL}${ENDPOINT} → HTTP $CODE"
  test "$CODE" = "200"
done

echo "Smoke tests passed"
