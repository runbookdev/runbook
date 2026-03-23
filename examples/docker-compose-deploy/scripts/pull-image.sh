#!/usr/bin/env bash
set -euo pipefail
# Usage: pull-image.sh <compose_file> <service> <version>
COMPOSE_FILE="$1"
SERVICE="$2"
VERSION="$3"

docker compose -f "$COMPOSE_FILE" pull "$SERVICE"
echo "Pulled $SERVICE:$VERSION"
