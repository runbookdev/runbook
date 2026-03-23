#!/usr/bin/env bash
set -euo pipefail
# Usage: apply-migration.sh <run_id>
WORKDIR="/tmp/runbook-demo-$1"

echo "Applying migration 0002_add_payments_table..."
sleep 1
echo "tables: users, orders, products, payments" > "$WORKDIR/db-state"
echo "$(date -u '+%Y-%m-%dT%H:%M:%SZ')  0002_add_payments_table  applied" \
  >> "$WORKDIR/migration.log"

echo "Migration complete"
echo "DB state: $(cat "$WORKDIR/db-state")"
