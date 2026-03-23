#!/usr/bin/env bash
set -euo pipefail
# Usage: run-migrations.sh <compose_file> <service>
COMPOSE_FILE="$1"
SERVICE="$2"

docker compose -f "$COMPOSE_FILE" run --rm "$SERVICE" \
  sh -c "migrate -path /app/migrations -database \$DATABASE_URL up"
echo "Migrations applied"
