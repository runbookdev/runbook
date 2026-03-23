#!/usr/bin/env bash
set -euo pipefail
# Usage: prune-images.sh
RECLAIMED=$(docker image prune -f | grep "Total reclaimed" || echo "0B reclaimed")
echo "Image prune: $RECLAIMED"
