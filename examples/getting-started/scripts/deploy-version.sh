#!/usr/bin/env bash
set -euo pipefail
# Usage: deploy-version.sh <run_id> <runbook_version>
WORKDIR="/tmp/runbook-demo-$1"
NEW_VERSION="$2"

PREV=$(cat "$WORKDIR/current-version")
echo "Stopping service v$PREV (graceful drain, up to 5 s)..."
sleep 1
echo "Service v$PREV stopped"

echo "$NEW_VERSION" > "$WORKDIR/current-version"
echo "status: running" > "$WORKDIR/service-status"

echo "Service v$NEW_VERSION started"
