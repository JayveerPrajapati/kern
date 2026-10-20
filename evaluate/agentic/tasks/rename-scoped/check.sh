#!/bin/sh
# Deterministic gate for rename-scoped. cwd = the agent's workspace copy.
set -e

go build ./...
go test ./...

# The cache constructor is renamed, exactly once, and the old name is gone.
test "$(grep -c 'func Open' cache/cache.go)" -eq 1 || { echo "FAIL: cache.Open missing"; exit 1; }
if grep -q 'func New' cache/cache.go; then echo "FAIL: cache.New still defined"; exit 1; fi
if grep -rq 'cache\.New' --include='*.go' .; then echo "FAIL: stale cache.New reference"; exit 1; fi

# The unrelated queue.New is untouched: still defined, still called twice
# from outside its package.
grep -q 'func New' queue/queue.go || { echo "FAIL: queue.New was renamed or removed"; exit 1; }
n=$(grep -rn 'queue\.New()' --include='*.go' . | wc -l | tr -d ' ')
test "$n" -eq 2 || { echo "FAIL: queue.New callers changed (found $n, want 2)"; exit 1; }
