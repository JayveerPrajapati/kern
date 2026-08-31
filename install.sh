#!/bin/sh
# kern installer — downloads prebuilt binaries from GitHub Releases.
#
#   curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh | sh
#
# Behavior:
#   * Defaults to the latest release; pin with KERN_VERSION=v1.2.3.
#   * Installs to ~/.local/bin by default, or $KERN_INSTALL_DIR if set.
#   * Downloads prebuilt tarballs; falls back to `go install` if a platform
#     has no prebuilt asset or curl/wget is unavailable but go is present.
#   * Never runs as root-required system installs; stays in user space.
#
# Distribution note: replace JayveerPrajapati below with your GitHub username (or run
# scripts/retarget.sh which rewrites this file for you).

set -u

OWNER="${KERN_REPO_OWNER:-JayveerPrajapati}"
REPO="${KERN_REPO:-${OWNER}/kern}"
VERSION="${KERN_VERSION:-latest}"
PREFIX="${KERN_INSTALL_DIR:-${HOME}/.local/bin}"

get_version() {
  if [ "$VERSION" = "latest" ]; then
    if command -v curl >/dev/null 2>&1; then
      curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
        grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' |
        sed 's/.*"\([^"]*\)".*/\1/' |
        head -1
    else
      wget -qO- "https://api.github.com/repos/${REPO}/releases/latest" 2>/dev/null |
        grep -o '"tag_name"[[:space:]]*:[[:space:]]*"[^"]*"' |
        sed 's/.*"\([^"]*\)".*/\1/' |
        head -1
    fi
  else
    echo "$VERSION"
  fi
}

os_arch() {
  os=$(uname -s | tr '[:upper:]' '[:lower:]')
  arch=$(uname -m)
  # Windows under Git-Bash / MSYS / Cygwin reports MINGW64_NT / MSYS_NT /
  # CYGWIN_NT; map it to "windows". Only amd64 has a prebuilt asset today.
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

go_install() {
  if ! command -v go >/dev/null 2>&1; then
    echo "kern: prebuilt asset for this platform is unavailable and 'go' is not installed." >&2
    echo "kern: install Go (https://go.dev/dl/) or download a release from https://github.com/${REPO}/releases" >&2
    return 1
  fi
  echo "kern: falling back to 'go install github.com/${REPO}/cmd/kern@${VERSION}'"
  go install "github.com/${REPO}/cmd/kern@${VERSION}"
  go install "github.com/${REPO}/cmd/kern-mcp@${VERSION}"
  go install "github.com/${REPO}/cmd/kern-server@${VERSION}"
  # go install places both into $(go env GOPATH)/bin, which is frequently NOT
  # on PATH and NOT the requested PREFIX. Copy both into PREFIX so the rest of
  # the install (PATH check, auto-wire, auto-index) sees them at the canonical
  # location — otherwise a go_install fallback silently never wires into agents.
  gobin="$(go env GOPATH)/bin"
  if [ -f "$gobin/kern" ] && [ -f "$gobin/kern-mcp" ] && [ -f "$gobin/kern-server" ]; then
    mkdir -p "$PREFIX"
    cp "$gobin/kern" "$PREFIX/kern"
    cp "$gobin/kern-mcp" "$PREFIX/kern-mcp"
    cp "$gobin/kern-server" "$PREFIX/kern-server"
    chmod +x "$PREFIX/kern" "$PREFIX/kern-mcp" "$PREFIX/kern-server"
    # macOS quarantine/signing: same treatment as the prebuilt path so a
    # go-installed kern-mcp is also runnable by agents on first launch.
    if [ "$(uname -s)" = "Darwin" ]; then
      if command -v xattr >/dev/null 2>&1; then
        xattr -dr com.apple.quarantine "$PREFIX/kern" "$PREFIX/kern-mcp" "$PREFIX/kern-server" 2>/dev/null || true
      fi
      if command -v codesign >/dev/null 2>&1; then
        codesign --force --sign - "$PREFIX/kern" "$PREFIX/kern-mcp" "$PREFIX/kern-server" 2>/dev/null || true
      fi
    fi
    echo "kern: copied binaries to $PREFIX (go install output was in $gobin)."
  else
    echo "kern: installed via go install, but could not find binaries in $gobin."
    echo "kern: ensure \$(go env GOPATH)/bin is on your PATH and run 'kern setup' in your project."
  fi
  echo "kern: next step: run 'kern setup' in your project to wire kern into your agents."
  return 0
}

# wire() auto-detects installed agents and wires kern's MCP server + kern-first
# rules into each, then auto-indexes the project. It runs `kern setup --detect
# --global` (see that command's docs for the full behavior). It is idempotent —
# re-running never duplicates entries. Called from BOTH the prebuilt-install
# path and the go_install fallback so a fallback install still wires agents
# (previously go_install returned without ever running setup, so nothing was
# wired and MCP was broken).
wire() {
  local kern_bin="$1"
  local proj_root
  if [ ! -x "$kern_bin" ]; then
    echo "kern: binary not executable at $kern_bin; skipping auto-wire." >&2
    echo "  run 'kern setup --detect --global' manually in your project root." >&2
    return 0
  fi
  proj_root="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
  echo "auto-wiring kern into detected agents in: $proj_root (and globally)"
  if "$kern_bin" setup --detect --root "$proj_root" --global 2>&1; then
    echo "kern: auto-wiring complete. Run 'kern setup --check' to verify."
  else
    echo "kern: auto-wiring skipped (no agents detected or setup failed)."
    echo "  run 'kern setup --detect --global' manually in your project root."
  fi
echo
return 0
}

# verify checks the downloaded tarball against the release's SHA256SUMS asset.
# It is best-effort: older releases without a SHA256SUMS file, entries missing
# from it, or hosts without a sha256 tool simply skip verification.
verify() {
  file="$1"; dir="$2"; tag="$3"
  sums_url="https://github.com/${REPO}/releases/download/${tag}/SHA256SUMS"
  if ! download "$sums_url" "$dir/SHA256SUMS"; then
    return 0
  fi
  if command -v sha256sum >/dev/null 2>&1; then
    sum="sha256sum"
  elif command -v shasum >/dev/null 2>&1; then
    sum="shasum -a 256"
  else
    return 0
  fi
  expected=$(grep -F "  ${file}" "$dir/SHA256SUMS" | awk '{print $1}')
  if [ -z "$expected" ]; then
    return 0
  fi
  actual=$($sum "$dir/$file" | awk '{print $1}')
  if [ "$actual" != "$expected" ]; then
    echo "kern: checksum mismatch for ${file} (expected ${expected}, got ${actual})" >&2
    return 1
  fi
  echo "kern: checksum ok (${actual})"
  return 0
}

main() {
  platform="$(os_arch)" || { echo "kern: unsupported platform $(uname -s)/$(uname -m)"; go_install || exit 1; wire "$PREFIX/kern"; exit 0; }
  tag="$(get_version)"
  [ -z "$tag" ] && { echo "kern: could not resolve release version"; go_install || exit 1; wire "$PREFIX/kern"; exit 0; }

  # Windows ships a .zip (kern-windows-amd64.zip); everything else a .tar.gz.
  if [ "${platform%-*}" = "windows" ]; then
    file="kern-${platform}.zip"
    exe=".exe"
  else
    file="kern-${platform}.tar.gz"
    exe=""
  fi
  url="https://github.com/${REPO}/releases/download/${tag}/${file}"
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  echo "kern: downloading ${tag} (${file})"
  if ! download "$url" "$tmpdir/${file}"; then
    echo "kern: no prebuilt asset for ${platform} at ${tag}" >&2
    go_install || exit 1
    wire "$PREFIX/kern"
    exit 0
  fi
  if ! verify "$file" "$tmpdir" "$tag"; then
    echo "kern: aborting install (checksum verification failed)" >&2
    exit 1
  fi

  mkdir -p "$PREFIX"
  if [ "${platform%-*}" = "windows" ]; then
    # Windows prebuilt is a zip; extract with unzip, else PowerShell (Git-Bash
    # usually has unzip; fall back to pwsh/powershell Expand-Archive).
    if command -v unzip >/dev/null 2>&1; then
      (cd "$tmpdir" && unzip -q "$file")
    elif command -v powershell >/dev/null 2>&1; then
      powershell -NoProfile -Command "Expand-Archive -LiteralPath '$tmpdir/$file' -DestinationPath '$tmpdir' -Force"
    elif command -v pwsh >/dev/null 2>&1; then
      pwsh -NoProfile -Command "Expand-Archive -LiteralPath '$tmpdir/$file' -DestinationPath '$tmpdir' -Force"
    else
      echo "kern: no unzip/powershell available to extract $file" >&2
      go_install || exit 1
      wire "$PREFIX/kern"
      exit 0
    fi
  else
    tar -xzf "$tmpdir/${file}" -C "$tmpdir"
  fi
  cp "$tmpdir/kern-${platform}/kern${exe}" "$PREFIX/kern${exe}"
  cp "$tmpdir/kern-${platform}/kern-mcp${exe}" "$PREFIX/kern-mcp${exe}"
  cp "$tmpdir/kern-${platform}/kern-server${exe}" "$PREFIX/kern-server${exe}"
  chmod +x "$PREFIX/kern${exe}" "$PREFIX/kern-mcp${exe}" "$PREFIX/kern-server${exe}"

  # macOS Gatekeeper kills unsigned binaries with SIGKILL (exit 137) even
  # though os.Stat sees the file; re-sign with an ad-hoc signature so the
  # copy is executable.
  if [ "${platform%-*}" = "darwin" ] && command -v codesign >/dev/null 2>&1; then
    codesign --force --sign - "$PREFIX/kern" "$PREFIX/kern-mcp" "$PREFIX/kern-server"
  fi
  # macOS quarantine: the downloaded tarball carries the com.apple.quarantine
  # xattr, which Gatekeeper propagates onto the extracted copies. codesign alone
  # does NOT clear it, so the freshly-installed kern-mcp would still be killed
  # on first agent launch (exit 137). Removing the xattr is required for a
  # release install to actually run. Best-effort (xattr may not exist on all
  # platforms / filesystems).
  if [ "${platform%-*}" = "darwin" ] && command -v xattr >/dev/null 2>&1; then
    xattr -dr com.apple.quarantine "$PREFIX/kern" "$PREFIX/kern-mcp" "$PREFIX/kern-server" 2>/dev/null || true
  fi

  echo "installed: $PREFIX/kern${exe} ($tag)"

  # Ensure the install dir is on PATH so `kern` / `kern-mcp` / `kern-server` run from anywhere
  # and agents auto-launch the MCP server by bare name. Best-effort, idempotent:
  # only appends the export line once per shell rc, never duplicates, and honors
  # a per-run opt-out (KERN_NO_PATH=1).
  if [ "${KERN_NO_PATH:-0}" != "1" ]; then
    ensure_path "$PREFIX"
  else
    case ":$PATH:" in
      *":$PREFIX:"*) ;;
      *) echo "note: add $PREFIX to your PATH:  export PATH=\"$PREFIX:\$PATH\"" ;;
    esac
  fi
  echo

  # Auto-wire into detected agents (and auto-index the project). Extracted into
  # the wire() helper so both the prebuilt path and the go_install fallback run
  # it — a fallback install must wire agents exactly like a prebuilt one.
  wire "$PREFIX/kern${exe}"

  echo "kern is ready. Open your project in any agent — it auto-indexes on first use."
  echo "  (run from your project root; 'kern setup --check' shows current wiring)"
}

# ensure_path appends `export PATH="$PREFIX:$PATH"` to the user's shell rc files
# (idempotently — one line per rc, only when the dir is not already present).
# This makes `kern` / `kern-mcp` / `kern-server` reachable from any directory and lets agents
# resolve the global MCP server by name after an upgrade. Best-effort: files
# that cannot be written are skipped with a note, never fatal.
ensure_path() {
  prefix="$1"
  case ":$PATH:" in
    *":$prefix:"*) return 0 ;; # already on PATH for this shell
  esac
  line="export PATH=\"$prefix:\$PATH\""
  marker="# added by kern installer"
  for rc in "$HOME/.profile" "$HOME/.bashrc" "$HOME/.zshrc"; do
    [ -f "$rc" ] || [ "$rc" = "$HOME/.profile" ] || continue
    if [ ! -f "$rc" ]; then
      # create .profile if it does not exist
      if ! touch "$rc" 2>/dev/null; then continue; fi
    fi
    if grep -qF "PATH=\"$prefix" "$rc" 2>/dev/null; then
      echo "kern: $prefix already on PATH in $rc (skipped)"
      continue
    fi
    {
      printf '\n%s\n' "$marker"
      printf '%s\n' "$line"
    } >> "$rc" 2>/dev/null && {
      echo "kern: added $prefix to PATH in $rc (open a new shell to pick it up)"
      export PATH="$prefix:$PATH"
    } || echo "kern: could not write $rc (add '$line' to your PATH manually)" >&2
  done
  return 0
}

main
