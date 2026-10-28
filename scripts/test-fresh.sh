#!/usr/bin/env bash
# test-fresh — run the fast suite in a throwaway CLONE with no .kern store.
#
# Why a clone and not a worktree: a linked worktree's `.git` is a file, and
# several tests (MCP root discovery) correctly require a real repository
# directory; using a worktree would fail them for the wrong reason. A clone is
# also the exact CI shape.
#
# Why: a test that depends on a warm local index (or a daemon it left behind)
# passes on the developer's machine and fails on a clean checkout. The
# 2026-10-28 guard regression was exactly that: `go test -short ./...` was red
# in a fresh clone and green in the warm tree.
#
# Usage: make test-fresh   (or: bash scripts/test-fresh.sh)
set -euo pipefail

root=$(git rev-parse --show-toplevel)
tmp=$(mktemp -d "${TMPDIR:-/tmp}/kern-fresh.XXXXXX")

cleanup() { rm -rf "$tmp"; }
trap cleanup EXIT

git clone --quiet --no-hardlinks "$root" "$tmp/repo"
cd "$tmp/repo"

# .kern/boundaries.json is tracked (project configuration); the index store is
# what must not exist. Remove anything a hook or concurrent run left behind,
# then prove it is gone before spending five minutes testing.
rm -f .kern/index.sqlite .kern/index.json .kern/index.json.snap
for f in .kern/index.sqlite .kern/index.json .kern/index.json.snap; do
  if [ -e "$f" ]; then
    echo "test-fresh: refused — a warm index is still present at $f" >&2
    exit 1
  fi
done

KERN_DAEMON=0 go test -short -timeout 12m ./...
