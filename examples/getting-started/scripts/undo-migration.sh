#!/usr/bin/env bash
set -euo pipefail
# Usage: undo-migration.sh <run_id>
WORKDIR="/tmp/runbook-demo-$1"

cp "$WORKDIR/backup/db-state.bak" "$WORKDIR/db-state"
rm -f "$WORKDIR/migration.log"
echo "Migration rolled back — DB state: $(cat "$WORKDIR/db-state")"
