#!/bin/sh
# release-smoke.sh — post-packaging verification of release artifacts (plan B3).
#
# Verifies what a user actually downloads, not the development binary:
#   1. SHA256SUMS covers every archive and every checksum matches.
#   2. The current-platform archive extracts cleanly.
#   3. `kern version` / `kern --version` / `kern-mcp version` run and
#      report the expected stamp.
#   4. A live MCP stdio handshake: initialize + tools/list. The default
#      surface and the KERN_MCP_FULL=1 surface must both expose 32 tools
#      (the full catalog is the default surface since 2026-10-23).
#   5. The shipped binary indexes a real repo (`kern index .`) and serves
#      a search query with results.
#   6. `kern doctor` runs against that repo with zero [fail] findings
#      (warnings are environment-dependent and allowed).
#
# Usage:
#   scripts/release-smoke.sh [--dir DIR] [--repo REPO] [--version V]
#     --dir     artifact directory (default: bin/ — after `make release`)
#     --repo    kern checkout to index/search/doctor against (default: cwd)
#     --version expected version stamp (e.g. v1.2.3); when given, the
#               binaries must report exactly this stamp
#
# Exit codes follow the CLI contract: 0 ok, 1 findings (smoke failure),
# 2 usage.
set -u

DIR=bin
REPO=$(pwd)
VERSION=""
fail() { echo "release-smoke: FAIL: $*" >&2; exit 1; }
usage() { echo "usage: scripts/release-smoke.sh [--dir DIR] [--repo REPO] [--version V]" >&2; exit 2; }

while [ $# -gt 0 ]; do
  case "$1" in
    --dir) [ $# -ge 2 ] || usage; DIR=$2; shift 2 ;;
    --repo) [ $# -ge 2 ] || usage; REPO=$2; shift 2 ;;
    --version) [ $# -ge 2 ] || usage; VERSION=$2; shift 2 ;;
    *) usage ;;
  esac
done

[ -d "$DIR" ] || fail "artifact directory $DIR does not exist (run: make release VERSION=<tag>)"
[ -f "$DIR/SHA256SUMS" ] || fail "$DIR/SHA256SUMS missing"
[ -d "$REPO/cmd/kern" ] || fail "$REPO is not a kern checkout (cmd/kern not found)"

SHA256SUM=sha256sum
command -v sha256sum >/dev/null 2>&1 || SHA256SUM="shasum -a 256"

os=$(uname -s); arch=$(uname -m)
case "$os" in Darwin) os=darwin ;; Linux) os=linux ;; *) fail "unsupported host OS $os (smoke runs on darwin/linux)" ;; esac
case "$arch" in x86_64|amd64) arch=amd64 ;; aarch64|arm64) arch=arm64 ;; *) fail "unsupported host arch $arch" ;; esac

ARCHIVE="$DIR/kern-$os-$arch.tar.gz"
[ -f "$ARCHIVE" ] || fail "current-platform archive missing: $ARCHIVE"

echo "==> 1/6 verifying checksums ($(basename "$ARCHIVE") + friends)"
# shellcheck disable=SC2164
(cd "$DIR" && $SHA256SUM -c SHA256SUMS >/dev/null) || fail "SHA256SUMS verification failed — an archive is corrupted or the manifest is stale"

echo "==> 2/6 inventory"
for f in kern-linux-amd64.tar.gz kern-linux-arm64.tar.gz kern-darwin-amd64.tar.gz \
         kern-darwin-arm64.tar.gz kern-windows-amd64.zip kern-windows-arm64.zip; do
  if [ -f "$DIR/$f" ]; then echo "    ok  $f"; else echo "    --  $f (not in this artifact set)"; fi
done

SMOKE=$(mktemp -d) || fail "mktemp failed"
trap 'rm -rf "$SMOKE"' EXIT INT TERM
mkdir -p "$SMOKE/bin"
tar -xzf "$ARCHIVE" -C "$SMOKE/bin" || fail "extraction failed: $ARCHIVE"
K="$SMOKE/bin/kern"; KM="$SMOKE/bin/kern-mcp"
[ -x "$K" ] || fail "extracted kern is not executable"
[ -x "$KM" ] || fail "extracted kern-mcp is not executable"

echo "==> 3/6 version stamps"
KVER=$("$K" version 2>&1) || fail "kern version failed: $KVER"
echo "    $KVER"
"$K" --version >/dev/null 2>&1 || fail "kern --version failed"
"$KM" version >/dev/null 2>&1 || fail "kern-mcp version failed"
if [ -n "$VERSION" ]; then
  case "$KVER" in *"$VERSION"*) ;; *) fail "version stamp mismatch: expected $VERSION in: $KVER" ;; esac
fi

echo "==> 4/6 MCP stdio handshake + tool surfaces"
handshake() { # handshake <env-extra> <outfile>
  _env=$1; _out=$2
  printf '%s\n%s\n%s\n' \
    '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2024-11-05","capabilities":{},"clientInfo":{"name":"release-smoke","version":"0"}}}' \
    '{"jsonrpc":"2.0","method":"notifications/initialized"}' \
    '{"jsonrpc":"2.0","id":2,"method":"tools/list"}' \
    | env KERN_PRELOAD=0 $_env "$KM" > "$_out" 2>/dev/null
}
command -v python3 >/dev/null 2>&1 || fail "python3 required for the handshake probe"
tool_names() { # tool_names <outfile> -> one name per line, from the tools/list response
  python3 - "$1" <<'PYEOF'
import json, sys
for line in open(sys.argv[1]):
    line = line.strip()
    if not line:
        continue
    try:
        msg = json.loads(line)
    except ValueError:
        continue
    if msg.get("id") == 2 and isinstance(msg.get("result"), dict) \
            and isinstance(msg["result"].get("tools"), list):
        for t in msg["result"]["tools"]:
            print(t.get("name", ""))
PYEOF
}
handshake "" "$SMOKE/default.json" || fail "default-surface handshake failed"
handshake "KERN_MCP_FULL=1" "$SMOKE/full.json" || fail "full-surface handshake failed"
default_n=$(tool_names "$SMOKE/default.json" | grep -c .)
full_n=$(tool_names "$SMOKE/full.json" | grep -c .)
[ "$default_n" -eq 32 ] || fail "default MCP surface is $default_n tools, expected 32"
[ "$full_n" -eq 32 ] || fail "full MCP surface (KERN_MCP_FULL=1) is $full_n tools, expected 32"
for t in kern_arch kern_graph; do
tool_names "$SMOKE/default.json" | grep -qx "$t" || fail "$t missing from the default surface"
tool_names "$SMOKE/full.json" | grep -qx "$t" || fail "$t missing from the full surface"
done
echo "    default surface: $default_n tools · full surface: $full_n tools"

echo "==> 5/6 index + search on the real repo"
# shellcheck disable=SC2164
(cd "$REPO" && "$K" index . >/dev/null) || fail "kern index . failed on $REPO"
SEARCH_OUT=$(cd "$REPO" && "$K" search checkIndexShadow 2>/dev/null) || fail "kern search returned no match (wiring regression?)"
[ -n "$SEARCH_OUT" ] || fail "kern search produced empty output"
echo "    search checkIndexShadow: $(printf '%s\n' "$SEARCH_OUT" | grep -c .) result line(s)"

echo "==> 6/6 doctor (zero [fail] findings)"
DOC=$(cd "$REPO" && "$K" doctor --json 2>/dev/null) || fail "kern doctor exited non-zero"
FAILS=$(printf '%s\n' "$DOC" | python3 -c '
import json, sys
try:
    findings = json.load(sys.stdin)
except ValueError:
    sys.exit(3)
for f in [f for f in findings if f.get("level") == "fail"]:
    print("[fail] %s: %s" % (f.get("check"), f.get("detail")))
sys.exit(1 if any(f.get("level") == "fail" for f in findings) else 0)
')
rc=$?
[ "$rc" -eq 3 ] && fail "kern doctor --json emitted invalid JSON"
if [ "$rc" -eq 1 ]; then
  printf '%s\n' "$FAILS" >&2
  fail "doctor reported [fail] findings (above)"
fi
echo "    doctor: no [fail] findings"

echo "release-smoke: PASS — $ARCHIVE verified (checksums, stamps, handshake 32/32, index+search, doctor)"
