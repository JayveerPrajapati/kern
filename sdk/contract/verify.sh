#!/bin/sh
# verify.sh — run the SDK contract suites in sequence (Python, TypeScript).
# Every suite asserts the SHARED route-shape fixture
# (sdk/contract/tools_call.json — POST /v1/tools/{name}) against its own
# client, so a route-shape change fails the first suite that catches it and
# this script stops there (fail on first failure). NOTE (2026-10-19): the
# live REST passthrough route (and the Go suite that anchored it, sdk/go) was
# removed with the enterprise web console; the Python/TS clients are kept as
# contract-pinned references against the shared fixture, and the Python
# suite's live cross-check skips with that reason.
#
# Usage: sh sdk/contract/verify.sh   (from anywhere; resolves the repo root)
set -eu

# $0 is <repo>/sdk/contract/verify.sh (or a relative path to it): two levels
# up from the script's dir is the repo root.
repo_root=$(CDPATH= cd -- "$(dirname "$0")/../.." && pwd)

echo "==> Python contract suite (sdk/python)"
(cd "$repo_root/sdk/python" && python3 -m unittest test_client)

echo "==> TypeScript contract suite (sdk/typescript)"
(cd "$repo_root/sdk/typescript" && npm run build && npm test)

echo "SDK contract suites: all passed"