#!/usr/bin/env bash
set -euo pipefail

TARGET="${1:-}"

if [ -n "$TARGET" ]; then
  echo "=== 1. Pre-Edit Risk Assessment for $TARGET ==="
  kern impact "$TARGET" || true
  echo ""
fi

echo "=== 2. Running Verification Gates (security, architecture) ==="
kern verify security,architecture || {
  echo ""
  echo "[!] Gates failed. Run 'kern heal --task \"<what the code should do>\"' to trigger auto-repair in a sandbox."
  exit 1
}

echo "[✓] Verification gates passed."
