#!/bin/sh
# kern installer — step-by-step, verifiable, with lifecycle operations.
#
#   curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh | sh
#
# Operations (first argument; default: install):
#   install.sh [install]   download + verify + install (fail-loud per step)
#   install.sh upgrade      compare installed vs target, swap if older
#   install.sh update       alias of upgrade
#   install.sh uninstall    remove binaries (+ optional config), --yes skips prompt
#   install.sh status       installed version, path, platform
#
# Every step prints a "✔ ..." line as it succeeds, so a `curl | sh` run
# shows exactly what happened (opencode-installer style). Any failure
# aborts with "✖" and a fix hint — never a silent partial install.
#
# Environment:
#   KERN_VERSION=v1.2.3     pin a release tag (default: latest; an explicit
#                           pin overrides KERN_CHANNEL entirely)
#   KERN_CHANNEL=stable     release channel for KERN_VERSION=latest (default:
#                           latest). latest = newest release, hotfixes
#                           included; stable = newest 3-component tag
#                           (4-component hotfixes like v0.9.9.1 excluded);
#                           any other value = a regex over release tag names
#                           (e.g. '^v0\.9\.' — newest match wins).
#                           Regex channels are matched with grep -E (POSIX
#                           ERE), the same semantics the Go side (kern update
#                           --channel, RE2) implements. KEEP REGEX CHANNELS IN
#                           THE COMMON SUBSET: RE2-only constructs — \d, \w,
#                           \s, \b and (?i)-style flags — resolve differently
#                           under ERE and are REJECTED with an error here and
#                           in the Go resolver. Use the ERE spelling instead:
#                           [0-9] for \d, [A-Za-z0-9_] for \w, case-insensitive
#                           classes like [A-Za-z] instead of (?i).
#   KERN_FORCE=1            override a refused upgrade (local-build overwrite,
#                           downgrade) — set by `kern update --force`
#   KERN_PIN=1              deliberate-pin consent for a downgrade (set by
#                           `kern update --pin <tag>`; honored by preflight)
#   KERN_INSTALL_DIR=dir    install prefix (default: ~/.local/bin)
#   KERN_REPO / KERN_REPO_OWNER   GitHub source (retarget for forks)
#   KERN_BASE_URL=url       release-download base (default: the GitHub repo
#                           URL; override to a mirror or file:// test fixture)
#   KERN_API_URL=url        GitHub API base for release resolution (default:
#                           https://api.github.com/repos/${REPO}; override to
#                           a mirror or file:// test fixture)
#   KERN_OS / KERN_ARCH     testing overrides for platform detection
#   KERN_SKIP_DISPATCH=1    load functions only, skip the operation dispatch
#                           (for sourcing the script in test harnesses)
#   KERN_NO_PATH=1          skip the shell-rc PATH management
#
# Behavior kept from the previous installer: prebuilt tarballs with a
# `go install` fallback when no asset exists for the platform, both old
# (kern-<os>-<arch>/ subdirectory) and new (archive-root) layouts, PATH
# rc management, and agent auto-wiring after install.
#
# macOS Gatekeeper: after every copy the installer re-signs (adhoc) and
# strips quarantine/provenance xattrs, then PROBES kern-mcp with a real
# MCP initialize handshake — a Gatekeeper kill (Killed: 9 / exit 137)
# becomes a deterministic per-install verdict instead of a dead MCP
# server at first agent launch.

set -u

OWNER="${KERN_REPO_OWNER:-JayveerPrajapati}"
REPO="${KERN_REPO:-${OWNER}/kern}"
VERSION="${KERN_VERSION:-latest}"
CHANNEL="${KERN_CHANNEL:-latest}"
PREFIX="${KERN_INSTALL_DIR:-${HOME}/.local/bin}"
BASE_URL="${KERN_BASE_URL:-https://github.com/${REPO}}"
API_URL="${KERN_API_URL:-https://api.github.com/repos/${REPO}}"

OP="install"
ASSUME_YES=0
for arg in "$@"; do
  case "$arg" in
    install|upgrade|update|uninstall|status) OP="$arg" ;;
    --yes|-y) ASSUME_YES=1 ;;
    *) echo "kern: unknown argument '$arg' (operations: install | upgrade | update | uninstall | status)" >&2; exit 1 ;;
  esac
done
[ "$OP" = "update" ] && OP="upgrade"

step() { printf '  ✔ %s\n' "$*"; }
warn() { printf '  ⚠ %s\n' "$*"; }
die()  { printf '  ✖ %s\n' "$*" >&2; exit 1; }

# os_arch prints <os>-<arch> for the release-asset naming, or fails.
os_arch() {
  os="${KERN_OS:-$(uname -s | tr '[:upper:]' '[:lower:]')}"
  arch="${KERN_ARCH:-$(uname -m)}"
  # Windows under Git-Bash / MSYS / Cygwin reports MINGW64_NT / MSYS_NT /
  # CYGWIN_NT; map it to "windows". amd64 and arm64 have prebuilt assets.
  case "$os" in
    mingw*|msys*|cygwin*) os="windows" ;;
  esac
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) return 1 ;;
  esac
  [ "$os" = "linux" ] || [ "$os" = "darwin" ] || [ "$os" = "windows" ] || return 1
  echo "${os}-${arch}"
}

# api_get fetches a GitHub API URL and prints the response body; curl first,
# wget fallback — the same pattern the rest of the script uses.
api_get() {
  url="$1"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" 2>/dev/null
  else
    wget -qO- "$url" 2>/dev/null
  fi
}

# tag_names extracts the "tag_name" fields from a GitHub releases API JSON
# response (single object or array), one per line, in API order (newest
# first).
tag_names() {
  grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)".*/\1/'
}

# pick_highest [stable] reads release tag names (one per line) on stdin and
# prints the highest per the 4-component numeric tuple order (missing 4th =
# 0) — the same order the Go Compare uses. With "stable", only tags with
# exactly three numeric components qualify, so 4-component hotfixes like
# v0.9.9.1 are excluded (mirrors the Go ResolveChannel). Prerelease/build
# suffixes are stripped for the tuple compare; at a tuple tie the bare
# release beats its own prerelease, then the first-seen line wins (the API
# lists newest-first). Non-numeric bodies are skipped.
pick_highest() {
  stable="${1:-}"
  awk -v stable="$stable" '
    function num(s,   r) {
      if (s !~ /^[0-9]+$/) return -1
      r = s + 0
      return r
    }
    {
      t = $0
      sub(/^v/, "", t)
      sub(/\+.*$/, "", t)
      had_pre = index(t, "-") > 0
      sub(/-.*$/, "", t)
      n = split(t, f, ".")
      a = num(f[1]); b = num(f[2]); c = num(f[3]); d = (n >= 4 ? num(f[4]) : 0)
      if (a < 0 || b < 0 || c < 0 || d < 0) next
      if (stable == "stable" && n != 3) next
      if (best == "" || a > ba || (a == ba && (b > bb || (b == bb && (c > bc || (c == bc && d > bd)))))) {
        best = $0; ba = a; bb = b; bc = c; bd = d; best_pre = had_pre
      } else if (a == ba && b == bb && c == bc && d == bd && best_pre && !had_pre) {
        best = $0; best_pre = 0   # bare release beats its prerelease at a tie
      }
    }
    END { if (best != "") print best }
  '
}

# channel_re2_only reports whether a channel regex uses RE2-only constructs
# that POSIX ERE (grep -E) resolves differently — \d, \w, \s, \b and any
# (?...) form — so the installer fails loudly instead of silently resolving a
# different release set than the Go side (finding 9). The ERE spellings are
# [0-9], [A-Za-z0-9_], [A-Za-z] etc.
channel_re2_only() {
  case "$1" in
    *'\'[dDwWsSbB]*|*'(?'*) return 0 ;;
  esac
  return 1
}

# get_version resolves the target release tag. An explicit KERN_VERSION pin
# always wins (the channel is ignored entirely). Otherwise the channel
# decides: "latest" keeps the /releases/latest endpoint behavior; "stable"
# and regex channels list up to 100 releases and pick the highest matching
# tag locally (the same semantics as the Go ResolveChannel). An empty result
# — no match, an invalid regex, or a fetch failure — is the caller's
# fail-closed signal (install_release falls back to go install or dies).
get_version() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION"
    return 0
  fi
  if [ "$CHANNEL" = "latest" ]; then
    api_get "${API_URL}/releases/latest" | tag_names | head -1
    return 0
  fi
  tags="$(api_get "${API_URL}/releases?per_page=100" | tag_names)"
  [ -n "$tags" ] || return 0
  if [ "$CHANNEL" = "stable" ]; then
    printf '%s\n' "$tags" | pick_highest stable
  else
    if channel_re2_only "$CHANNEL"; then
      die "channel '$CHANNEL' uses RE2-only regex syntax (\\d, \\w, \\s, \\b, (?i), ...) that this installer's POSIX-ERE grep does not support; use the ERE spelling instead — [0-9] for \\d, [A-Za-z0-9_] for \\w, case-insensitive classes like [A-Za-z]"
      return 0
    fi
    printf '%s\n' "$tags" | grep -E "$CHANNEL" 2>/dev/null | pick_highest
  fi
}

download() {
  url="$1"; out="$2"
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "$url" -o "$out"
  elif command -v wget >/dev/null 2>&1; then
    wget -qO "$out" "$url"
  else
    return 1
  fi
}

# installed_version prints the version of the kern binary in $PREFIX
# (e.g. v0.9.7), or nothing when not installed.
installed_version() {
  [ -x "$PREFIX/kern" ] || return 0
  # "kern v0.9.7-..." -> "v0.9.7" (first version-shaped token)
  "$PREFIX/kern" version 2>/dev/null | sed -n 's/^kern[[:space:]]*\(v[0-9][^[:space:]]*\).*/\1/p' | head -1
}

# verify checks the downloaded archive against the release's SHA256SUMS
# asset. Fail-closed: a present-but-mismatched checksum is FATAL (tampering),
# and so is any state in which the checksum cannot be confirmed — a missing
# SHA256SUMS asset or entry (older releases) or a host without a sha256 tool
# aborts the install instead of warning and skipping (finding L3). The
# go-install/source-build fallback path never calls verify, so it is
# unaffected by this fail-closed contract.
verify() {
  file="$1"; dir="$2"; tag="$3"
  if command -v sha256sum >/dev/null 2>&1; then
    sum="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    sum="shasum -a 256"
  else
    die "no sha256 tool (sha256sum/shasum) on this machine — cannot verify ${file}; refusing to install an unverifiable download (build from source with 'go install github.com/${REPO}/cmd/kern@${VERSION}')"
  fi
  if ! download "${BASE_URL}/releases/download/${tag}/SHA256SUMS" "$dir/SHA256SUMS"; then
    die "no SHA256SUMS asset for ${tag} — cannot verify ${file}; refusing to install an unverifiable download (build from source with 'go install github.com/${REPO}/cmd/kern@${VERSION}', or pick a release that ships checksums)"
  fi
  expected=$(grep -F "  ${file}" "$dir/SHA256SUMS" | awk '{print $1}')
  if [ -z "$expected" ]; then
    die "${file} not listed in SHA256SUMS for ${tag} — cannot verify the download; refusing to install an unverifiable artifact (build from source with 'go install github.com/${REPO}/cmd/kern@${VERSION}', or pick a release that ships checksums)"
  fi
  actual=$($sum "$dir/$file" | awk '{print $1}')
  if [ "$actual" != "$expected" ]; then
    die "checksum mismatch for ${file} (expected ${expected}, got ${actual}) — download may be corrupted or tampered"
  fi
  step "Checksum verified (${actual})"
}

# gatekeeper_prepare re-signs and strips the Gatekeeper xattrs that make
# macOS SIGKILL freshly copied binaries (Killed: 9 / exit 137, empty
# output). Mirrors the Makefile install target. No-op elsewhere.
gatekeeper_prepare() {
  [ "$1" = "darwin" ] || return 0
  command -v codesign >/dev/null 2>&1 || { warn "codesign not found — skipping re-sign"; return 0; }
  codesign --force --sign - "$PREFIX/kern" "$PREFIX/kern-mcp" "$PREFIX/kern-server" 2>/dev/null || true
  if command -v xattr >/dev/null 2>&1; then
    xattr -dr com.apple.quarantine "$PREFIX/kern" "$PREFIX/kern-mcp" "$PREFIX/kern-server" 2>/dev/null || true
    xattr -dr com.apple.provenance "$PREFIX/kern" "$PREFIX/kern-mcp" "$PREFIX/kern-server" 2>/dev/null || true
  fi
}

# probe_mcp sends a one-shot MCP initialize handshake and asserts a JSON-RPC
# response — the deterministic verdict that Gatekeeper did not kill the
# binary. The pipe (printf | kern-mcp | head) closes on response so the
# server exits on stdin EOF; no hang.
probe_mcp() {
  mcp_bin="$1"
  [ -x "$mcp_bin" ] || return 1
  resp=$(printf '%s\n' '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"kern-installer","version":"1.0"}}}' \
    | "$mcp_bin" 2>/dev/null | head -c 400) || resp=""
  case "$resp" in
    *'"jsonrpc"'*) return 0 ;;
  esac
  return 1
}

# probe_with_retry probes kern-mcp once; on failure it re-runs the re-sign/
# xattr choreography and probes once more; a second failure prints the
# manual fix commands and aborts.
probe_with_retry() {
  os="$1"
  if probe_mcp "$PREFIX/kern-mcp"; then
    step "kern-mcp MCP handshake OK"
    return 0
  fi
  [ "$os" = "darwin" ] || die "kern-mcp did not answer the MCP initialize handshake"
  warn "kern-mcp probe failed — retrying after re-sign/xattr"
  gatekeeper_prepare darwin
  if probe_mcp "$PREFIX/kern-mcp"; then
    step "kern-mcp MCP handshake OK (after re-sign)"
    return 0
  fi
  cat >&2 <<EOF
  ✖ kern-mcp is being killed by macOS Gatekeeper. Run manually, then re-probe:
       codesign --force --sign - $PREFIX/kern $PREFIX/kern-mcp $PREFIX/kern-server
       xattr -dr com.apple.quarantine $PREFIX/kern-mcp
       xattr -dr com.apple.provenance $PREFIX/kern-mcp
     then verify:  printf '{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"probe","version":"1.0"}}}' | $PREFIX/kern-mcp
EOF
  exit 1
}

go_install() {
  if ! command -v go >/dev/null 2>&1; then
    echo "kern: prebuilt asset for this platform is unavailable and 'go' is not installed." >&2
    echo "kern: install Go (https://go.dev/dl/) or download a release from https://github.com/${REPO}/releases" >&2
    return 1
  fi
  warn "falling back to 'go install github.com/${REPO}/cmd/kern@${VERSION}'"
  go install "github.com/${REPO}/cmd/kern@${VERSION}" &&
    go install "github.com/${REPO}/cmd/kern-mcp@${VERSION}" &&
    go install "github.com/${REPO}/cmd/kern-server@${VERSION}" || return 1
  gobin="$(go env GOPATH)/bin"
  if [ -f "$gobin/kern" ] && [ -f "$gobin/kern-mcp" ] && [ -f "$gobin/kern-server" ]; then
    mkdir -p "$PREFIX"
    cp "$gobin/kern" "$PREFIX/kern"
    cp "$gobin/kern-mcp" "$PREFIX/kern-mcp"
    cp "$gobin/kern-server" "$PREFIX/kern-server"
    chmod +x "$PREFIX/kern" "$PREFIX/kern-mcp" "$PREFIX/kern-server"
    gatekeeper_prepare darwin
    step "Installed kern kern-mcp kern-server to $PREFIX (go install fallback)"
    return 0
  fi
  echo "kern: installed via go install, but could not find binaries in $gobin." >&2
  echo "kern: ensure \$(go env GOPATH)/bin is on your PATH and run 'kern setup' in your project." >&2
  return 1
}

# wire auto-detects installed agents and wires kern's MCP server + kern-first
# rules into each (kern setup --detect --global). Idempotent. Kept from the
# previous installer so both paths (prebuilt, go fallback) wire agents.
wire() {
  kern_bin="$1"
  if [ ! -x "$kern_bin" ]; then
    warn "binary not executable at $kern_bin; skipping auto-wire — run 'kern setup --detect --global' manually"
    return 0
  fi
  proj_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
  echo "  … auto-wiring kern into detected agents in: $proj_root (and globally)"
  if "$kern_bin" setup --detect --root "$proj_root" --global 2>&1; then
    step "agent wiring complete — 'kern setup --check' shows current wiring"
  else
    warn "auto-wiring skipped (no agents detected or setup failed) — run 'kern setup --detect --global' manually"
  fi
}

# install_release performs the full download → verify → install → probe
# flow. When $1 = "upgrade", existing binaries are backed up first and
# restored if any later step fails.
install_release() {
  mode="$1"

  platform="$(os_arch)" || {
    warn "no prebuilt asset pattern for $(uname -s)/$(uname -m)"
    go_install || die "no prebuilt asset and go-install fallback failed"
    wire "$PREFIX/kern"
    return 0
  }
  os="${platform%-*}"
  step "Detected ${platform}"

  tag="$(get_version)"
  [ -n "$tag" ] || { go_install || die "could not resolve a release version (and go install failed)"; wire "$PREFIX/kern"; return 0; }
  step "Target release: ${tag}"

  if [ "$os" = "windows" ]; then
    file="kern-${platform}.zip"
    exe=".exe"
  else
    file="kern-${platform}.tar.gz"
    exe=""
  fi
  url="${BASE_URL}/releases/download/${tag}/${file}"
  tmpdir="$(mktemp -d)"
  install_ok=0
  backup_dir=""
  trap 'if [ "${install_ok:-0}" != "1" ] && [ -n "${backup_dir:-}" ]; then restore_backup 2>/dev/null; fi; rm -rf "$tmpdir"' EXIT

  printf '  … downloading %s\n' "$file"
  download "$url" "$tmpdir/${file}" || {
    warn "no prebuilt asset for ${platform} at ${tag}"
    go_install || die "download failed and go-install fallback failed"
    wire "$PREFIX/kern"
    return 0
  }
  size=$(wc -c < "$tmpdir/${file}" | tr -d ' ')
  step "Downloaded ${file} ($((size / 1024 / 1024)) MB)"

  verify "$file" "$tmpdir" "$tag"

  mkdir -p "$PREFIX"
  if [ "$os" = "windows" ]; then
    if command -v unzip >/dev/null 2>&1; then
      (cd "$tmpdir" && unzip -q "$file")
    elif command -v powershell >/dev/null 2>&1; then
      powershell -NoProfile -Command "Expand-Archive -LiteralPath '$tmpdir/$file' -DestinationPath '$tmpdir' -Force"
    elif command -v pwsh >/dev/null 2>&1; then
      pwsh -NoProfile -Command "Expand-Archive -LiteralPath '$tmpdir/$file' -DestinationPath '$tmpdir' -Force"
    else
      die "no unzip/powershell available to extract $file"
    fi
  else
    tar -xzf "$tmpdir/${file}" -C "$tmpdir" || die "extracting ${file} failed"
  fi
  # Release archives have used two layouts: <=v0.9.4 packaged a
  # kern-<os>-<arch>/ subdirectory; >=v0.9.5.2 ships binaries at the
  # archive root. Detect which one we extracted.
  src="$tmpdir"
  if [ -f "$tmpdir/kern-${platform}/kern${exe}" ]; then
    src="$tmpdir/kern-${platform}"
  fi
  if [ ! -f "$src/kern${exe}" ] || [ ! -f "$src/kern-mcp${exe}" ] || [ ! -f "$src/kern-server${exe}" ]; then
    die "extracted ${file} but expected binaries not found (looked in $src) — refusing to report success with nothing copied"
  fi
  step "Extracted kern, kern-mcp, kern-server"

  # Upgrade safety: back up the current binaries; the EXIT trap above
  # restores them on any failure path (die / probe exit / early return).
  if [ "$mode" = "upgrade" ] && [ -x "$PREFIX/kern" ]; then
    backup_dir="$tmpdir/backup"
    mkdir -p "$backup_dir"
    for b in kern kern-mcp kern-server; do
      [ -f "$PREFIX/$b${exe}" ] && cp "$PREFIX/$b${exe}" "$backup_dir/$b${exe}"
    done
  fi

  cp "$src/kern${exe}" "$PREFIX/kern${exe}" || die "copying kern to $PREFIX failed"
  cp "$src/kern-mcp${exe}" "$PREFIX/kern-mcp${exe}" || die "copying kern-mcp to $PREFIX failed"
  cp "$src/kern-server${exe}" "$PREFIX/kern-server${exe}" || die "copying kern-server to $PREFIX failed"
  chmod +x "$PREFIX/kern${exe}" "$PREFIX/kern-mcp${exe}" "$PREFIX/kern-server${exe}"
  step "Installed kern kern-mcp kern-server to $PREFIX"

  if [ "$os" = "darwin" ]; then
    gatekeeper_prepare darwin
    step "Re-signed for macOS Gatekeeper (codesign + xattr strip)"
  fi

  got="$($PREFIX/kern${exe} version 2>/dev/null | head -1)"
  case "$got" in
    kern\ v*) step "Verified: ${got}" ;;
    *) die "'$PREFIX/kern version' did not run cleanly (got: '${got:-empty}') — old binaries restored by the backup trap" ;;
  esac

  probe_with_retry "$os"

  if [ "${KERN_NO_PATH:-0}" != "1" ]; then
    ensure_path "$PREFIX"
  else
    case ":$PATH:" in
      *":$PREFIX:"*) ;;
      *) echo "  note: add $PREFIX to your PATH:  export PATH=\"$PREFIX:\$PATH\"" ;;
    esac
  fi

  install_ok=1
  echo
  wire "$PREFIX/kern${exe}"
  echo "kern is ready. Open your project in any agent — it auto-indexes on first use."
}

restore_backup() {
  [ -n "${backup_dir:-}" ] || return 0
  for b in kern kern-mcp kern-server; do
    [ -f "$backup_dir/$b${exe}" ] && cp "$backup_dir/$b${exe}" "$PREFIX/$b${exe}" && chmod +x "$PREFIX/$b${exe}"
  done
}

# remove_kern_section excises the kern-first block from an AGENTS.md the
# same way internal/setup's removeKernSection does: from the
# "# kern usage rules" heading up to (not including) the next H1 or EOF.
remove_kern_section() {
  awk '
    /^# kern usage rules/ { skip = 1; next }
    skip && /^# /        { skip = 0 }
    !skip                { print }
  ' "$1"
}

# ensure_path appends `export PATH="$PREFIX:$PATH"` to the user's shell rc
# files (idempotently — one line per rc, only when the dir is not already
# present). Best-effort: files that cannot be written are skipped with a
# note, never fatal.
ensure_path() {
  prefix="$1"
  case ":$PATH:" in
    *":$prefix:"*) return 0 ;;
  esac
  line="export PATH=\"$prefix:\$PATH\""
  marker="# added by kern installer"
  for rc in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
    [ -f "$rc" ] || [ "$rc" = "$HOME/.profile" ] || continue
    if [ ! -f "$rc" ]; then
      if ! touch "$rc" 2>/dev/null; then continue; fi
    fi
    if grep -qF "PATH=\"$prefix" "$rc" 2>/dev/null; then
      step "$prefix already on PATH in $rc"
      continue
    fi
    {
      printf '\n%s\n' "$marker"
      printf '%s\n' "$line"
    } >> "$rc" 2>/dev/null && {
      step "added $prefix to PATH in $rc (open a new shell to pick it up)"
      export PATH="$prefix:$PATH"
    } || warn "could not write $rc (add '$line' to your PATH manually)"
  done
  return 0
}

cmd_status() {
  platform="$(os_arch 2>/dev/null || echo "$(uname -s)-$(uname -m) (no prebuilt asset)")"
  echo "kern installer status"
  echo "  platform:      ${platform}"
  echo "  channel:       ${CHANNEL}"
  echo "  install dir:   $PREFIX"
  iv="$(installed_version)"
  if [ -n "$iv" ]; then
    echo "  installed:     $iv ($PREFIX/kern)"
  else
    echo "  installed:     (not installed in $PREFIX)"
  fi
  latest="$(get_version 2>/dev/null)"
  [ -n "$latest" ] && echo "  latest release: ${latest}"
  for b in kern kern-mcp kern-server; do
    if [ -x "$PREFIX/$b" ]; then
      echo "  $b: present"
    else
      echo "  $b: MISSING"
    fi
  done
}

cmd_uninstall() {
  platform="$(os_arch 2>/dev/null)" || platform=""
  os="${platform%-*}"
  if [ "$os" = "windows" ]; then
    exe=".exe"
  else
    exe=""
  fi
  echo "kern uninstall — removes:"
  for b in kern kern-mcp kern-server; do
    [ -f "$PREFIX/$b${exe}" ] && echo "  $PREFIX/$b${exe}"
  done
  [ -f "$HOME/AGENTS.md" ] && grep -q "^# kern usage rules" "$HOME/AGENTS.md" 2>/dev/null && echo "  kern-first block in ~/AGENTS.md"
  [ -d "$HOME/.config/kern" ] && echo "  ~/.config/kern"
  [ -d "$HOME/.cache/kern" ] && echo "  ~/.cache/kern"
  if [ "$ASSUME_YES" != "1" ]; then
    printf 'Proceed? [y/N] '
    read -r answer
    case "$answer" in
      y|Y|yes|YES) ;;
      *) echo "aborted — nothing removed"; exit 0 ;;
    esac
  fi
  for b in kern kern-mcp kern-server; do
    rm -f "$PREFIX/$b${exe}" && step "removed $PREFIX/$b${exe}"
  done
  if [ -f "$HOME/AGENTS.md" ] && grep -q "^# kern usage rules" "$HOME/AGENTS.md" 2>/dev/null; then
    cleaned="$(remove_kern_section "$HOME/AGENTS.md")"
    if [ -n "$(printf '%s' "$cleaned" | tr -d '[:space:]')" ]; then
      printf '%s\n' "$cleaned" > "$HOME/AGENTS.md"
    else
      rm -f "$HOME/AGENTS.md"
    fi
    step "removed kern-first block from ~/AGENTS.md (other content preserved)"
  fi
  if [ -d "$HOME/.config/kern" ]; then
    rm -rf "$HOME/.config/kern" && step "removed ~/.config/kern"
  fi
  if [ -d "$HOME/.cache/kern" ]; then
    rm -rf "$HOME/.cache/kern" && step "removed ~/.cache/kern"
  fi
  # PATH rc lines (marker + export) — best-effort, idempotent. grep -vF into
  # a temp file keeps this portable across GNU/BSD sed.
  for rc in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
    [ -f "$rc" ] || continue
    if grep -qF "# added by kern installer" "$rc" 2>/dev/null; then
      grep -vF "# added by kern installer" "$rc" | grep -vF "export PATH=\"$PREFIX:" > "$rc.kern-new" 2>/dev/null \
        && mv "$rc.kern-new" "$rc" \
        && step "removed kern PATH lines from $rc"
    fi
  done
  echo "kern uninstalled. (Per-project wiring: delete .mcp.json / AGENTS.md in each project root.)"
}

# preflight_shim is the deliberately dumb fallback for OLD kern binaries
# that predate the hidden `update --preflight` subcommand (curl|sh users on
# a previous release). It can only PROVE "target is newer" via a strict
# 4-component numeric tuple compare; anything it cannot prove — a
# local-build-shaped installed version (Makefile short-hash stamp, "dev",
# or an installed binary whose version is unreadable), a prerelease/build
# suffix, a non-numeric field — is refused unless KERN_FORCE=1. Fail-closed:
# an unprovable upgrade is an aborted upgrade, never a silent overwrite.
preflight_shim() {
pf_cur="$1"
pf_tgt="$2"
if [ "${KERN_FORCE:-0}" = "1" ]; then
warn "preflight (shim): KERN_FORCE=1 — proceeding with ${pf_cur} -> ${pf_tgt}"
return 0
fi
# An installed binary whose version is not readable (installed_version
# found nothing, e.g. a Makefile short-hash stamp) is the #561 overwrite
# class: refuse instead of reinstalling over it.
if [ -z "$pf_cur" ]; then
die "refusing to overwrite an unidentifiable installed build — rebuild via 'make build && make install', or re-run with --force"
fi
# Release-shaped check: a version not starting with 'v' + digits is a
# local build (or unverifiable) — refuse. The trailing '*' lets
# prerelease/build shapes through to the numeric check below, which
# refuses them with the accurate "cannot verify" reason.
case "$pf_cur" in
v[0-9]*.[0-9]*.[0-9]*)
;;
*)
die "refusing to overwrite a local build ('${pf_cur}') — rebuild via 'make build && make install', or re-run with --force"
;;
esac
pf_c="${pf_cur#v}"
pf_t="${pf_tgt#v}"
case "$pf_t" in
[0-9]*.[0-9]*.[0-9]*)
;;
*)
die "cannot verify target '${pf_tgt}' against '${pf_cur}' — re-run with --force to override"
;;
esac
pf_i=0
while [ "$pf_i" -lt 4 ]; do
pf_n=$((pf_i + 1))
pf_cf="$(printf '%s' "$pf_c" | cut -d. -f"$pf_n")"
pf_tf="$(printf '%s' "$pf_t" | cut -d. -f"$pf_n")"
[ -n "$pf_cf" ] || pf_cf=0
[ -n "$pf_tf" ] || pf_tf=0
# Strict numeric fields only: a prerelease/build suffix ("3-rc1",
# "3+build") makes the tuple incomparable — refuse rather than guess.
case "$pf_cf" in
*[!0-9]*) die "cannot verify installed version '${pf_cur}' — re-run with --force to override" ;;
esac
case "$pf_tf" in
*[!0-9]*) die "cannot verify target '${pf_tgt}' — re-run with --force to override" ;;
esac
if [ "$pf_cf" -gt "$pf_tf" ]; then
die "downgrade refused: ${pf_cur} -> ${pf_tgt} — pass --pin ${pf_tgt} to downgrade deliberately"
fi
if [ "$pf_cf" -lt "$pf_tf" ]; then
return 0 # target strictly newer — allow
fi
pf_i=$((pf_i + 1))
done
step "already at ${pf_tgt} — nothing to do"
return 0
}
cmd_upgrade() {
target="$(get_version)"
[ -n "$target" ] || die "could not resolve the target version"
# Fresh install only when nothing is installed: a present-but-unreadable
# installed version (the #561 hash-stamp case) must NOT be treated as
# "not installed" — the preflight gate below decides it instead.
if [ ! -x "$PREFIX/kern" ]; then
warn "kern is not installed in $PREFIX — running a fresh install"
install_release upgrade
return 0
fi
current="$(installed_version)"
# Preflight gate (Stage A release-channel policy): the installed binary —
# when it carries the hidden `update --preflight` subcommand — decides
# (installed, target) itself: exit 0 = allow/no-op, 3 = deny (abort unless
# KERN_FORCE=1), 2 = old binary without the subcommand (fall back to the
# dumb shim above). The verdict output is captured so a deny's reason is
# surfaced to the user instead of swallowed. Every use of $target/$current
# is quoted: they arrive from the network (tag) or `kern version` output
# and must never be word-split or re-interpreted (hostile-input discipline).
pf_out="$("$PREFIX/kern" update --preflight "$target" 2>&1)"
pf_rc=$?
if [ "$pf_rc" = "0" ]; then
[ -n "$pf_out" ] && printf '%s\n' "$pf_out"
else
if [ "$pf_rc" = "3" ]; then
if [ "${KERN_FORCE:-0}" != "1" ]; then
printf '%s\n' "$pf_out" >&2
die "update refused by kern preflight (reason above); re-run with --force to overwrite"
fi
warn "preflight refused — continuing because KERN_FORCE=1"
elif [ "$pf_rc" = "2" ]; then
preflight_shim "$current" "$target"
else
die "kern preflight failed (exit $pf_rc); re-run with --force to override"
fi
fi
# Normalize (strip leading v) for the no-op equality check below.
cur="${current#v}"; tgt="${target#v}"
if [ "$cur" = "$tgt" ]; then
step "already at ${target} — nothing to do"
return 0
fi
echo "kern upgrade: ${current} -> ${target}"
install_release upgrade
}

# The dispatch runs when the script is executed as a program. Sourcing the
# script with KERN_SKIP_DISPATCH=1 (a testing override, like KERN_OS/KERN_ARCH)
# loads the functions only, so a harness can exercise get_version /
# pick_highest / tag_names against a fixed tag list without side effects.
if [ "${KERN_SKIP_DISPATCH:-0}" != "1" ]; then
case "$OP" in
  status)    cmd_status ;;
  uninstall) cmd_uninstall ;;
  upgrade)   cmd_upgrade ;;
  install)   install_release install ;;
esac
fi
