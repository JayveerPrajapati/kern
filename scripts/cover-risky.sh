#!/usr/bin/env bash
# cover-risky.sh — the security-surface coverage gate.
#
# These packages hold the parts of kern where a defect is a security or data
# incident rather than a cosmetic one:
#
#   internal/mcp/exec      the exec/sandbox firewall, diff, validate, heal
#   internal/mcp/evidence  signed evidence bundles and citation anchoring
#   internal/mcp/memory    the project brain (writes lessons to disk)
#   internal/mcp/bridge    forwarding calls to EXTERNAL MCP servers
#   internal/mcp/compose   multi-step tool pipelines (arbitrary tool chains)
#   internal/mcp/graph     the graph tool bodies every agent reads through
#
# Behaviour-level tests only: line chasing inflates the number without covering
# a decision.
#
# Floors: 70% everywhere except exec. exec's sandbox/validate/build SUCCESS
# paths need a working OS sandbox (seatbelt on darwin, landlock on Linux); on a
# minimal container those tests skip by design rather than fake a pass, which
# costs ~13 points. Its guard paths — the half that decides whether an
# untrusted command runs at all — are platform-independent and covered. Measured
# 2026-10-27 on darwin/arm64: exec 76.9, evidence 85.9, memory 88.1,
# bridge 94.4, compose 95.7, graph 74.5.
set -uo pipefail

DEFAULT_MIN="${KERN_RISKY_COVER_MIN:-70}"

PKGS=(
  ./internal/mcp/exec/
  ./internal/mcp/evidence/
  ./internal/mcp/memory/
  ./internal/mcp/bridge/
  ./internal/mcp/compose/
  ./internal/mcp/graph/
  # The command-safety oracles. `execclassify` is the governed-exec decision,
  # `guardrule` the agent-shell decision, and the other two are the shared
  # rule tables both call. A silent miss here is an ungoverned delete.
  ./internal/execclassify/
  ./internal/guardrule/
  ./internal/destrules/
  ./internal/destructive/
)

# floor PKG → percentage; anything unlisted uses DEFAULT_MIN.
floor_for() {
  case "$1" in
    ./internal/mcp/exec/) echo "${KERN_RISKY_COVER_EXEC_MIN:-60}" ;;
    *) echo "$DEFAULT_MIN" ;;
  esac
}

fail=0
printf '%-28s %8s %8s\n' "package" "coverage" "floor"
for p in "${PKGS[@]}"; do
  min=$(floor_for "$p")
  # No -short: the sandbox/validate/build paths are the point, and they only
  # run at full length. The six packages together stay around 25s.
  out=$(go test -count=1 -cover "$p" 2>&1 | grep -o 'coverage: [0-9.]*% of statements' | head -1)
  pct=$(printf '%s' "$out" | sed 's/coverage: //; s/% of statements//')
  if [ -z "$pct" ]; then
    printf '%-28s %8s %7s%%  (no coverage reported)\n' "$p" "?" "$min"
    fail=1
    continue
  fi
  ok=$(awk -v a="$pct" -v b="$min" 'BEGIN{print (a+0 >= b+0) ? "1" : "0"}')
  printf '%-28s %7s%% %7s%%\n' "$p" "$pct" "$min"
  [ "$ok" = "1" ] || fail=1
done

if [ "$fail" -ne 0 ]; then
  echo "cover-risky: FAIL — a security-surface package fell below its floor" >&2
  exit 1
fi
echo "cover-risky: PASS — every security-surface package at or above its floor"
