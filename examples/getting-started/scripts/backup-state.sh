#!/usr/bin/env bash
set -euo pipefail
# Usage: backup-state.sh <run_id>
WORKDIR="/tmp/runbook-demo-$1"

cp "$WORKDIR/current-version" "$WORKDIR/backup/current-version.bak"
cp "$WORKDIR/db-state"        "$WORKDIR/backup/db-state.bak"

echo "Backup written to $WORKDIR/backup/"
echo "  current-version → $(cat "$WORKDIR/backup/current-version.bak")"
echo "  db-state        → $(cat "$WORKDIR/backup/db-state.bak")"
