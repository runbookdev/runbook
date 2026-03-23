#!/usr/bin/env bash
set -euo pipefail
# Usage: check-env.sh <env>
ENV="$1"
case "$ENV" in
  staging|production) echo "Environment '$ENV' is valid" ;;
  *) echo "Unknown environment '$ENV' — expected staging or production"; exit 1 ;;
esac
