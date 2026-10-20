#!/usr/bin/env bash
# touched-packages.sh — the scoped test selector behind `make test-touched`.
#
# Prints the unique go packages containing files changed by the working
# tree (staged + unstaged vs HEAD); when the tree is clean it falls back
# to the last commit's diff (HEAD~1..HEAD). Files that pin tests in OTHER
# packages map to those packages explicitly — the 687ec36 lesson: the
# opencode plugin and AGENTS.md are read by internal/setup AND
# internal/architecture, so touching them must run both.
#
# Output: one space-separated line of package paths for `go test`, or a
# note on stderr when nothing maps. Docs map to the doc-gate packages
# (internal/setup runs the doc-state gates, internal/architecture the
# ledger/AGENTS parity).
set -euo pipefail
cd "$(dirname "$0")/.."

if [ -n "$(git status --porcelain)" ]; then
  # -uall: untracked dirs expand to files, so a new package's .go files
  # are seen individually instead of as an opaque "?? dir/" entry.
  changed=$(git -c status.showUntrackedFiles=all status --porcelain | awk '{print $NF}')
else
  changed=$(git diff --name-only HEAD~1 HEAD --)
fi

pkgs=()
add() { pkgs+=("$1"); }

for f in $changed; do
  case "$f" in
    *testdata*)
      # fixture code, not a testable package
      ;;
    *.go)
      add "./$(dirname "$f")"
      ;;
    .opencode/plugins/kern.ts|internal/setup/assets/*|.opencode/*)
      add "./internal/setup"
      ;;
    AGENTS.md)
      add "./internal/setup"
      add "./internal/architecture"
      ;;
    ARCHITECTURE.md|docs/architecture/*)
      add "./internal/architecture"
      ;;
    docs/tool-catalog.md)
      add "./internal/setup"
      add "./internal/mcp"
      ;;
    docs/*|*.md)
      add "./internal/setup"
      add "./internal/architecture"
      ;;
  esac
done

if [ ${#pkgs[@]} -eq 0 ]; then
  echo "touched-packages: no changed files map to a package (nothing to test)" >&2
  exit 0
fi

# unique, sorted
printf '%s\n' "${pkgs[@]}" | sort -u | tr '\n' ' '
echo
