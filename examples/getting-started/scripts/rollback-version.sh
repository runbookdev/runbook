#!/usr/bin/env bash
set -euo pipefail
# Usage: rollback-version.sh <run_id>
WORKDIR="/tmp/runbook-demo-$1"

CURRENT=$(cat "$WORKDIR/current-version" 2>/dev/null || echo "unknown")
echo "Stopping service v$CURRENT..."
cp "$WORKDIR/backup/current-version.bak" "$WORKDIR/current-version"
PREV=$(cat "$WORKDIR/current-version")
echo "Service rolled back to v$PREV"
