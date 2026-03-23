#!/usr/bin/env bash
set -euo pipefail
# Usage: restart-containers.sh <compose_file> <service> <version>
COMPOSE_FILE="$1"
SERVICE="$2"
VERSION="$3"

docker compose -f "$COMPOSE_FILE" up -d --no-deps --force-recreate "$SERVICE"
echo "Container restarted with $SERVICE:$VERSION"
