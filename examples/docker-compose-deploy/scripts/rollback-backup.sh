#!/usr/bin/env bash
set -euo pipefail
# Usage: rollback-backup.sh <service> <db_container>
SERVICE="$1"
DB_CONTAINER="$2"

BACKUP_FILE=$(cat "/tmp/${SERVICE}-last-backup.txt" 2>/dev/null || true)
if [ -z "$BACKUP_FILE" ] || [ ! -f "$BACKUP_FILE" ]; then
  echo "No backup file found — skipping restore"
  exit 0
fi
echo "Restoring from $BACKUP_FILE"
gunzip -c "$BACKUP_FILE" | docker exec -i "$DB_CONTAINER" psql -U postgres
echo "Database restored"
