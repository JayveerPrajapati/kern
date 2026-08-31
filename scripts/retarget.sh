#!/bin/sh
# retarget.sh — repoint the project's module path and distribution
# placeholders from the dev values to your real GitHub repo.
#
#   ./scripts/retarget.sh github.com/yourname/kern
#
# Does, in one pass:
#   * go.mod module line
#   * all internal import paths (github.com/JayveerPrajapati/kern/internal/...)
#   * install.sh OWNER, homebrew/kern.rb OWNER, python/ pyproject + shim
#   * README references (github.com/JayveerPrajapati/kern, JayveerPrajapati)
#
# After running: `go build ./...`, commit, push to your new repo, tag v0.1.0,
# then use make release / the release workflow.

set -eu

MOD="${1:?usage: retarget.sh github.com/yourname/kern}"

if [ "$MOD" = "github.com/JayveerPrajapati/kern" ]; then
  echo "error: MOD must differ from the current module path" >&2
  exit 1
fi

OWNER="$(echo "$MOD" | cut -d/ -f2)"

echo "==> retargeting module path to $MOD (owner $OWNER)"

# Go sources: go.mod + import paths.
sed -i "s|^module github.com/JayveerPrajapati/kern$|module $MOD|" go.mod
grep -rl "github.com/JayveerPrajapati/kern/" --include="*.go" cmd internal .github 2>/dev/null | \
  while read -r f; do
    sed -i "s|github.com/JayveerPrajapati/kern/|$MOD/|g" "$f"
  done

# Distribution placeholders.
sed -i "s|JayveerPrajapati|$OWNER|g" install.sh
sed -i "s|JayveerPrajapati|$OWNER|g" homebrew/kern.rb
sed -i "s|JayveerPrajapati|$OWNER|g" python/pyproject.toml python/kern/_bootstrap.py python/README.md
sed -i "s|JayveerPrajapati|$OWNER|g" README.md
sed -i "s|github.com/JayveerPrajapati/kern|$MOD|g" README.md .github/workflows/release.yml install.sh homebrew/kern.rb python/pyproject.toml python/kern/_bootstrap.py python/README.md 2>/dev/null || true

echo "==> done. Verify with: go build ./... && go test ./..."