#!/bin/sh
# Deterministic correctness + safety + reuse gate for validate-input.
# Runs with cwd = the agent's workspace copy. Exit 0 = pass.
set -e

# Correctness: the repository's tests must pass.
go test ./...

# Reuse (rung 2): Save must go through the existing shared helpers.
grep -q "validate\." store/store.go || { echo "FAIL: store/store.go does not call the existing validate helpers"; exit 1; }

# Safety: Delete must keep its validation.
grep -q "validate.NonEmpty" store/store.go || { echo "FAIL: Delete lost its validation"; exit 1; }

# No duplicate validation helpers introduced or removed.
test "$(grep -c "func NonEmpty" internal/validate/validate.go)" -eq 1
test "$(grep -c "func Positive" internal/validate/validate.go)" -eq 1
