#!/bin/sh
# brew-release.sh — generate a Homebrew formula with real SHA256 sums.
#
# Usage:
#   ./scripts/brew-release.sh v1.2.3                # formula from the GitHub source tarball
#   ./scripts/brew-release.sh v1.2.3 /path/to/tarball
#
# Prints a kern.rb you can drop into your homebrew-tap Formula/ directory.
# If no tarball is given, it downloads the GitHub source tarball for the tag
# — the same bytes Homebrew will fetch and hash — so the sha256 matches.

set -eu

VERSION="${1:?usage: brew-release.sh <tag> [tarball]}"
TARBALL="${2:-}"

if [ -n "$TARBALL" ]; then
  if [ ! -f "$TARBALL" ]; then
    echo "error: tarball not found: $TARBALL" >&2
    exit 1
  fi
  SHA256="$(sha256sum "$TARBALL" | awk '{print $1}')"
  URL_TARBALL="file://$TARBALL"
else
  TMP="$(mktemp -d)"
  trap 'rm -rf "$TMP"' EXIT
  echo "fetching source tarball: https://github.com/${KERN_REPO:-JayveerPrajapati/kern}/archive/refs/tags/${VERSION}.tar.gz" >&2
  curl -fsSL "https://github.com/${KERN_REPO:-JayveerPrajapati/kern}/archive/refs/tags/${VERSION}.tar.gz" \
    -o "$TMP/kern.tar.gz"
  SHA256="$(sha256sum "$TMP/kern.tar.gz" | awk '{print $1}')"
  URL_TARBALL="$TMP/kern.tar.gz"
fi

sed -e "s|JayveerPrajapati/kern|${KERN_REPO:-JayveerPrajapati/kern}|g" \
    -e "s|__KERN_VERSION__|${VERSION}|g" \
    -e "s|__KERN_SHA256__|${SHA256}|" \
    homebrew/kern.rb

echo "# sha256: ${SHA256}" >&2
echo "# note: set KERN_REPO=<you>/kern to fill the repo placeholder." >&2
