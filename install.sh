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
#   KERN_VERSION=v1.2.3     pin a release tag (default: latest)
#   KERN_INSTALL_DIR=dir    install prefix (default: ~/.local/bin)
#   KERN_REPO / KERN_REPO_OWNER   GitHub source (retarget for forks)
#   KERN_BASE_URL=url       release-download base (default: the GitHub repo
#                           URL; override to a mirror or file:// test fixture)
#   KERN_OS / KERN_ARCH     testing overrides for platform detection
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
PREFIX="${KERN_INSTALL_DIR:-${HOME}/.local/bin}"
BASE_URL="${KERN_BASE_URL:-https://github.com/${REPO}}"

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

get_version() {
  if [ "$VERSION" != "latest" ]; then
    echo "$VERSION"
    return 0
  fi
  if command -v curl >/dev/null 2>&1; then
    curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
      grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)".*/\1/' | head -1
  else
    wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
      grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' | sed 's/.*"\([^"]*\)".*/\1/' | head -1
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
# asset. A present-but-mismatched checksum is FATAL (tampering); a missing
# SHA256SUMS asset or entry (older releases) or a host without a sha256
# tool only warns — matching the historical best-effort contract.
verify() {
  file="$1"; dir="$2"; tag="$3"
  if command -v sha256sum >/dev/null 2>&1; then
    sum="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    sum="shasum -a 256"
  else
    warn "no sha256 tool on this machine — skipping checksum verification"
    return 0
  fi
  if ! download "${BASE_URL}/releases/download/${tag}/SHA256SUMS" "$dir/SHA256SUMS"; then
    warn "no SHA256SUMS asset for ${tag} — skipping checksum verification"
    return 0
  fi
  expected=$(grep -F "  ${file}" "$dir/SHA256SUMS" | awk '{print $1}')
  if [ -z "$expected" ]; then
    warn "${file} not listed in SHA256SUMS — skipping checksum verification"
    return 0
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

cmd_upgrade() {
  current="$(installed_version)"
  target="$(get_version)"
  [ -n "$target" ] || die "could not resolve the target version"
  if [ -z "$current" ]; then
    warn "kern is not installed in $PREFIX — running a fresh install"
    install_release upgrade
    return 0
  fi
  # Normalize (strip leading v) for the comparison.
  cur="${current#v}"; tgt="${target#v}"
  if [ "$cur" = "$tgt" ]; then
    step "already at ${target} — nothing to do"
    return 0
  fi
  echo "kern upgrade: ${current} -> ${target}"
  install_release upgrade
}

case "$OP" in
  status)    cmd_status ;;
  uninstall) cmd_uninstall ;;
  upgrade)   cmd_upgrade ;;
  install)   install_release install ;;
esac
