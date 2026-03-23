#!/usr/bin/env bash
set -euo pipefail
# Usage: rollback-containers.sh <compose_file> <service>
COMPOSE_FILE="$1"
SERVICE="$2"

echo "Rolling back container to previous image"
docker compose -f "$COMPOSE_FILE" up -d --no-deps --force-recreate --scale "${SERVICE}=1" "$SERVICE"
docker compose -f "$COMPOSE_FILE" restart "$SERVICE"
echo "Rollback complete"
