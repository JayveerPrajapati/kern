#!/bin/sh
# verify.sh — run the three SDK contract suites in sequence (Go, Python,
# TypeScript). Every suite asserts the SHARED route-shape fixture
# (sdk/contract/tools_call.json — POST /v1/tools/{name}) against its own
# client, so a route-shape change fails the first suite that catches it and
# this script stops there (fail on first failure). The Go suite's in-process
# live route test is the always-on anchor; the Python/TS suites add an
# OPTIONAL live cross-check that starts a real `kern serve` from the repo
# when a Go toolchain is available (skipped gracefully otherwise).
#
# Usage: sh sdk/contract/verify.sh   (from anywhere; resolves the repo root)
set -eu

# $0 is <repo>/sdk/contract/verify.sh (or a relative path to it): two levels
# up from the script's dir is the repo root.
repo_root=$(CDPATH= cd -- "$(dirname -- "$0")/../.." && pwd)

echo "==> Go contract suite (sdk/go)"
(cd "$repo_root/sdk/go" && go test ./...)

echo "==> Python contract suite (sdk/python)"
(cd "$repo_root/sdk/python" && python3 -m unittest test_client)

echo "==> TypeScript contract suite (sdk/typescript)"
(cd "$repo_root/sdk/typescript" && npm run build && npm test)

echo "SDK contract suites: all passed"