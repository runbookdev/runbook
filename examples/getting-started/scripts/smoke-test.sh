#!/usr/bin/env bash
set -euo pipefail
# Usage: smoke-test.sh <run_id> <expected_version>
WORKDIR="/tmp/runbook-demo-$1"
EXPECTED_VERSION="$2"
PASS=0
FAIL=0

check() {
  local label="$1"; shift
  if "$@" > /dev/null 2>&1; then
    echo "  ✓ $label"; PASS=$((PASS + 1))
  else
    echo "  ✗ $label"; FAIL=$((FAIL + 1))
  fi
}

echo "Running smoke tests..."
check "service version is $EXPECTED_VERSION" \
  test "$(cat "$WORKDIR/current-version")" = "$EXPECTED_VERSION"
check "service status is running" \
  grep -q "running" "$WORKDIR/service-status"
check "migration log exists" \
  test -f "$WORKDIR/migration.log"
check "payments table in DB schema" \
  grep -q "payments" "$WORKDIR/db-state"

echo ""
echo "Results: $PASS passed, $FAIL failed"
test "$FAIL" -eq 0
