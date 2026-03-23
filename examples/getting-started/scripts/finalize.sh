#!/usr/bin/env bash
set -euo pipefail
# Usage: finalize.sh <run_id> <runbook_name> <runbook_version> <env> <user> <timestamp>
WORKDIR="/tmp/runbook-demo-$1"
RUNBOOK_NAME="$2"
RUNBOOK_VERSION="$3"
ENV="$4"
USER="$5"
TIMESTAMP="$6"

echo ""
echo "═══════════════════════════════════════"
echo "  Deployment Summary"
echo "═══════════════════════════════════════"
echo "  Service:     $RUNBOOK_NAME"
echo "  Version:     $(cat "$WORKDIR/current-version")"
echo "  Environment: $ENV"
echo "  Deployed by: $USER"
echo "  Timestamp:   $TIMESTAMP"
echo "  Run ID:      $1"
echo "═══════════════════════════════════════"
echo ""

rm -rf "$WORKDIR"
echo "Workspace cleaned up"
