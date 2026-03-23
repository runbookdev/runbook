#!/usr/bin/env bash
set -euo pipefail
# Usage: teardown-workspace.sh <run_id>
WORKDIR="/tmp/runbook-demo-$1"
rm -rf "$WORKDIR"
echo "Workspace removed"
