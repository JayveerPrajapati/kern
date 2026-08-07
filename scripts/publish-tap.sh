#!/bin/sh
# publish-tap.sh — create or update the Homebrew tap so
#   brew tap <owner>/tap && brew install kern
# works.
#
# Usage:
#   ./scripts/publish-tap.sh v1.2.3 [owner]
#
# Defaults to the repo's owner (JayveerPrajapati). The tap repo is named
# <owner>/homebrew-tap (that is where `brew tap <owner>/tap` looks); it is
# created public if it does not exist yet. The formula sha256 is computed
# from the *GitHub* source tarball (not a local `git archive`), because
# Homebrew downloads that tarball and verifies the checksum against the url.
#
# Requires: the `gh` CLI (authenticated) and `curl`.

set -eu

TAG="${1:?usage: publish-tap.sh <tag> [owner]}"
OWNER="${2:-$(gh repo view --json owner --jq .owner.login 2>/dev/null || echo JayveerPrajapati)}"
KERN_REPO="${KERN_REPO:-$OWNER/kern}"
TAP_REPO="$OWNER/homebrew-tap"

TMP="$(mktemp -d)"
trap 'rm -rf "$TMP"' EXIT

ARCHIVE="https://github.com/${KERN_REPO}/archive/refs/tags/${TAG}.tar.gz"
echo "==> fetching source archive: ${ARCHIVE}"
curl -fsSL "$ARCHIVE" -o "$TMP/source.tar.gz"
SHA256="$(sha256sum "$TMP/source.tar.gz" | awk '{print $1}')"
echo "==> source sha256: ${SHA256}"

echo "==> generating formula for ${TAG}"
sed -e "s|JayveerPrajapati/kern|${KERN_REPO}|g" \
    -e "s|v0.1.0|${TAG}|g" \
    -e "s|REPLACE_WITH_SOURCE_TARBALL_SHA256|${SHA256}|" \
    "$(dirname "$0")/../homebrew/kern.rb" > "$TMP/kern.rb"

cd "$TMP"
if gh repo view "$TAP_REPO" >/dev/null 2>&1; then
  echo "==> tap repo exists, cloning ${TAP_REPO}"
  gh repo clone "$TAP_REPO" tap
else
  echo "==> creating public tap repo ${TAP_REPO}"
  gh repo create "$TAP_REPO" --public --clone --description "Homebrew tap for kern" >/dev/null
  mv homebrew-tap tap
fi

mkdir -p tap/Formula
cp "$TMP/kern.rb" tap/Formula/kern.rb

cd tap
git add Formula/kern.rb
if git diff --cached --quiet; then
  echo "==> formula unchanged, nothing to push"
  exit 0
fi
git -c "user.name=${OWNER}" -c "user.email=${OWNER}@users.noreply.github.com" \
  commit -m "kern ${TAG}: update formula"
git push

echo "==> done: brew tap ${OWNER}/tap && brew install kern"
