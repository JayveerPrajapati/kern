#!/usr/bin/env bash
set -euo pipefail

LOG_PATH="${1:-}"
if [ -z "$LOG_PATH" ]; then
  echo "Usage: $0 <path_to_log_or_stacktrace>"
  exit 1
fi

if [ ! -f "$LOG_PATH" ]; then
  echo "Error: log file not found at $LOG_PATH"
  exit 1
fi

echo "=== 1. Compressing Log ==="
kern log "$LOG_PATH" || true

echo ""
echo "=== 2. Auto-SRE Triage & AST Symbol Mapping ==="
kern ops triage --log "$LOG_PATH" --non-interactive
