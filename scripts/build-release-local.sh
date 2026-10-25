#!/usr/bin/env bash
# scripts/build-release-local.sh — build release assets locally for any tag/version
#
# Usage:
#   scripts/build-release-local.sh [VERSION]
#
# Examples:
#   scripts/build-release-local.sh           # auto-detects current git tag or branch/commit
#   scripts/build-release-local.sh v0.9.12   # explicit version
set -euo pipefail

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$REPO_ROOT"

# Determine target version/tag:
# 1. First positional argument ($1) if provided
# 2. Exact git tag at HEAD (git describe --exact-match --tags)
# 3. Fallback to latest tag or short commit SHA
if [ -n "${1:-}" ]; then
  TAG="$1"
elif git describe --exact-match --tags HEAD >/dev/null 2>&1; then
  TAG="$(git describe --exact-match --tags HEAD)"
else
  TAG="$(git describe --tags --always 2>/dev/null || echo "dev")"
fi

DIST_DIR="$REPO_ROOT/dist"

echo "==> Preparing release build for version: ${TAG}"
rm -rf "$DIST_DIR"
mkdir -p "$DIST_DIR"

TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
  "windows/amd64"
  "windows/arm64"
)

LDFLAGS="-s -w -X main.version=${TAG} -X github.com/JayveerPrajapati/kern/internal/version.Version=${TAG}"

for target in "${TARGETS[@]}"; do
  os="${target%/*}"
  arch="${target#*/}"
  dir="$REPO_ROOT/kern-${os}-${arch}"
  rm -rf "$dir"
  mkdir -p "$dir"

  echo "--> Compiling ${os}/${arch}..."
  if [ "$os" = "windows" ]; then
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
      go build -buildvcs=false -trimpath -tags "sqlite,notreesitter" \
      -ldflags "$LDFLAGS" -o "$dir/kern.exe" ./cmd/kern
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
      go build -buildvcs=false -trimpath -tags "sqlite,notreesitter" \
      -ldflags "$LDFLAGS" -o "$dir/kern-mcp.exe" ./cmd/kern-mcp

    python3 -c "import zipfile, os, sys; out, d = sys.argv[1], sys.argv[2]; z = zipfile.ZipFile(out, 'w', zipfile.ZIP_DEFLATED); [z.write(os.path.join(d, f), f) for f in os.listdir(d)]; z.close()" "$DIST_DIR/kern-${os}-${arch}.zip" "$dir"
  else
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
      go build -buildvcs=false -trimpath -tags "sqlite,notreesitter" \
      -ldflags "$LDFLAGS" -o "$dir/kern" ./cmd/kern
    GOOS="$os" GOARCH="$arch" CGO_ENABLED=0 \
      go build -buildvcs=false -trimpath -tags "sqlite,notreesitter" \
      -ldflags "$LDFLAGS" -o "$dir/kern-mcp" ./cmd/kern-mcp

    tar -czf "$DIST_DIR/kern-${os}-${arch}.tar.gz" -C "$dir" .
  fi
  rm -rf "$dir"
done

echo "--> Generating dist/SHA256SUMS..."
(cd "$DIST_DIR" && sha256sum kern-*.tar.gz kern-*.zip) > "$DIST_DIR/SHA256SUMS"

echo "==> Done building release assets for ${TAG}!"
echo "Assets generated in: $DIST_DIR"
ls -lh "$DIST_DIR"
