#!/usr/bin/env bash
set -euo pipefail

SYMBOL="${1:-}"
if [ -z "$SYMBOL" ]; then
  echo "Usage: $0 <symbol_name>"
  exit 1
fi

echo "=== 1. Searching Symbol ==="
kern search "$SYMBOL" || true

echo ""
echo "=== 2. Call Graph & Adjacency ==="
kern explore "$SYMBOL" || true

echo ""
echo "=== 3. Dependency Neighborhood ==="
kern near "$SYMBOL" || true
