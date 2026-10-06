#!/bin/sh
# Deterministic correctness + reuse gate for fix-offbyone.
# Runs with cwd = the agent's workspace copy. Exit 0 = pass.
set -e

# Correctness: the repository's tests must pass.
go test ./...

# Reuse: the existing clamp helper must remain the single bounds authority —
# no copy-pasted duplicate may appear anywhere in the tree.
count=$(grep -rc "func clamp" . --include="*.go" | awk -F: '{s+=$2} END {print s}')
test "$count" -eq 1 || { echo "FAIL: duplicate clamp helper introduced (found $count)"; exit 1; }
