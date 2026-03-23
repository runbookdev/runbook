#!/usr/bin/env bash
set -euo pipefail
# Usage: backup-database.sh <service> <env> <db_container>
SERVICE="$1"
ENV="$2"
DB_CONTAINER="$3"

BACKUP_FILE="/tmp/${SERVICE}-${ENV}-$(date +%Y%m%dT%H%M%S).sql.gz"
docker exec "$DB_CONTAINER" pg_dumpall -U postgres | gzip > "$BACKUP_FILE"
echo "Backup written to $BACKUP_FILE"
echo "$BACKUP_FILE" > "/tmp/${SERVICE}-last-backup.txt"
