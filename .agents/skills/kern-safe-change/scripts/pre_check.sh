#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-}"

if [ -n "$TARGET" ]; then
  echo "=== 1. Pre-Edit Risk Assessment for $TARGET ==="
  kern pre-edit "$TARGET" || true
  echo ""
fi

echo "=== 2. Running Change-Firewall Gates (G0-G39) ==="
kern check || {
  echo ""
  echo "[!] Gates failed. Run 'kern fix' to trigger auto-repair in sandbox."
  exit 1
}

echo "[✓] All firewall gates passed."
