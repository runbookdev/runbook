#!/usr/bin/env bash
set -euo pipefail
# Usage: setup-workspace.sh <run_id> <runbook_version>
WORKDIR="/tmp/runbook-demo-$1"
VERSION="$2"

mkdir -p "$WORKDIR/backup"
echo "$VERSION"                                > "$WORKDIR/prev-version"
echo "0.9.0"                                   > "$WORKDIR/current-version"
echo "tables: users, orders, products"         > "$WORKDIR/db-state"
echo "status: running"                         > "$WORKDIR/service-status"

echo "Workspace created: $WORKDIR"
echo "Simulated service is running v$(cat "$WORKDIR/current-version")"
