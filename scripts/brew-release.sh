#!/bin/sh
# brew-release.sh — generate a Homebrew formula with real SHA256 sums.
#
# Usage:
#   ./scripts/brew-release.sh v1.2.3                # latest formula, local build
#   ./scripts/brew-release.sh v1.2.3 /path/to/tarball
#
# Prints a kern.rb you can drop into your homebrew-tap Formula/ directory.
# If no tarball is given, it builds one locally from the tag using
# `git archive` (requires the tag to exist locally).

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
  git archive --format=tar.gz -o "$TMP/kern.tar.gz" "$VERSION"
  SHA256="$(sha256sum "$TMP/kern.tar.gz" | awk '{print $1}')"
  URL_TARBALL="$TMP/kern.tar.gz"
fi

sed -e "s|<OWNER>|${KERN_REPO_OWNER:-OWNER}|g" \
    -e "s|v0.1.0|${VERSION}|g" \
    -e "s|REPLACE_WITH_SOURCE_TARBALL_SHA256|${SHA256}|" \
    homebrew/kern.rb

echo "# sha256: ${SHA256}" >&2
echo "# note: set KERN_REPO_OWNER=<you> to fill the OWNER placeholder." >&2
