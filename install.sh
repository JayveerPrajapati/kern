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
  case "$arch" in
    x86_64|amd64) arch="amd64" ;;
    aarch64|arm64) arch="arm64" ;;
    *) return 1 ;;
  esac
  [ "$os" = "linux" ] || [ "$os" = "darwin" ] || return 1
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
  # go install places both into the go bin dir; report it.
  echo "kern: installed via go install. Ensure \$(go env GOPATH)/bin is on your PATH."
  echo "kern: next step: run 'kern setup' in your project to wire kern into your agents."
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
  platform="$(os_arch)" || { echo "kern: unsupported platform $(uname -s)/$(uname -m)"; go_install || exit 1; exit 0; }
  tag="$(get_version)"
  [ -z "$tag" ] && { echo "kern: could not resolve release version"; go_install || exit 1; exit 0; }

  file="kern-${platform}.tar.gz"
  url="https://github.com/${REPO}/releases/download/${tag}/${file}"
  tmpdir="$(mktemp -d)"
  trap 'rm -rf "$tmpdir"' EXIT

  echo "kern: downloading ${tag} (${file})"
  if ! download "$url" "$tmpdir/${file}"; then
    echo "kern: no prebuilt asset for ${platform} at ${tag}" >&2
    go_install || exit 1
    exit 0
  fi
  if ! verify "$file" "$tmpdir" "$tag"; then
    echo "kern: aborting install (checksum verification failed)" >&2
    exit 1
  fi

  mkdir -p "$PREFIX"
  tar -xzf "$tmpdir/${file}" -C "$tmpdir"
  cp "$tmpdir/kern-${platform}/kern" "$PREFIX/kern"
  cp "$tmpdir/kern-${platform}/kern-mcp" "$PREFIX/kern-mcp"
  chmod +x "$PREFIX/kern" "$PREFIX/kern-mcp"

  # macOS Gatekeeper kills unsigned binaries with SIGKILL (exit 137) even
  # though os.Stat sees the file; re-sign with an ad-hoc signature so the
  # copy is executable.
  if [ "${platform%-*}" = "darwin" ] && command -v codesign >/dev/null 2>&1; then
    codesign --force --sign - "$PREFIX/kern" "$PREFIX/kern-mcp"
  fi

  echo "installed: $PREFIX/kern ($tag)"
  case ":$PATH:" in
    *":$PREFIX:"*) ;;
    *) echo "note: add $PREFIX to your PATH:  export PATH=\"$PREFIX:\$PATH\"" ;;
  esac
  echo

  # Auto-wire: detect installed agents and wire kern into them automatically.
  # This runs `kern setup --detect` which finds present agents (opencode,
  # claude, cursor, vscode, ...) and wires kern's MCP server + kern-first
  # rules into each. It is idempotent — re-running setup never duplicates
  # entries. If no agents are detected, the user is told how to wire manually.
  KERN_BIN="$PREFIX/kern"
  if [ -x "$KERN_BIN" ]; then
    # Detect the user's most likely project root: git toplevel of CWD, or CWD.
    PROJ_ROOT="$(git rev-parse --show-toplevel 2>/dev/null || pwd)"
    echo "auto-wiring kern into detected agents in: $PROJ_ROOT"
    if "$KERN_BIN" setup --detect --root "$PROJ_ROOT" 2>&1; then
      echo "kern: auto-wiring complete. Run 'kern setup --check' to verify."
    else
      echo "kern: auto-wiring skipped (no agents detected or setup failed)."
      echo "  run 'kern setup --detect' manually in your project root."
    fi
    echo
    # Auto-index the project so graph commands (walk, path, hubs, ...) work
    # immediately without a 3-minute cold start on first use.
    echo "indexing project (first run may take a minute)..."
    "$KERN_BIN" index "$PROJ_ROOT" 2>&1 || true
    echo
  fi

  echo "kern is ready. Use 'kern buddy' for a project onboarding digest."
  echo "  (run from your project root; 'kern setup --check' shows current wiring)"
}

main
