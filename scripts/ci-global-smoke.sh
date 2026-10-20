#!/usr/bin/env bash
# Machine-wide install smoke: wire -> try -> real git commit/push -> remove,
# all against a throwaway HOME so the caller's real config is never touched.
# Used by CI (.github/workflows/test.yml, global-install-smoke) on Linux, macOS
# and Windows (Git Bash); also runnable locally:
#
#   bash scripts/ci-global-smoke.sh            # builds kern from this checkout
#   bash scripts/ci-global-smoke.sh /path/kern # uses an existing binary
set -euo pipefail

REPO_ROOT=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
WORK=$(mktemp -d)
trap 'rm -rf "$WORK"' EXIT

KERN=${1:-}
if [ -z "$KERN" ]; then
  KERN="$WORK/bin/kern$(go env GOEXE)"
  mkdir -p "$WORK/bin"
  (cd "$REPO_ROOT" && CGO_ENABLED=0 go build -buildvcs=false -tags sqlite,notreesitter -o "$KERN" ./cmd/kern)
fi

export HOME="$WORK/home" USERPROFILE="$WORK/home"
export GIT_CONFIG_GLOBAL="$HOME/.gitconfig" GIT_CONFIG_NOSYSTEM=1
export XDG_CONFIG_HOME="$HOME/.config"
# Never inherit an operator's kern switches into the smoke.
unset KERN_BYPASS KERN_ENFORCE KERN_DISABLED KERN_BIN KERN_BINARY KERN_BYPASS_REASON
mkdir -p "$HOME"

git init -q --bare "$WORK/remote.git"
mkdir -p "$WORK/repo"
cd "$WORK/repo"
git init -q
git checkout -q -b main
printf 'module smoke\n\ngo 1.21\n' > go.mod
printf 'package smoke\n\nfunc B() int { return 1 }\nfunc A() int { return B() }\nfunc C() int { return B() + A() }\n' > a.go

repo_extras() { ls -A | grep -v -x -e .git -e go.mod -e a.go || true; }
commit() { git -c user.name=t -c user.email=t@example.com commit -q "$@"; }

echo "== wire machine-wide"
"$KERN" setup --global-only --global-rules --yes
"$KERN" setup --check

extra=$(repo_extras)
test -z "$extra" || { echo "FAIL: global wiring wrote repo files: $extra"; exit 1; }
test -f "$HOME/.kern/hooks/kern-guard.sh" || { echo "FAIL: guard script not installed"; exit 1; }
test -f "$HOME/.kern/git-hooks/pre-push" || { echo "FAIL: pre-push hook not installed"; exit 1; }
test -n "$(git config --global core.hooksPath)" || { echo "FAIL: core.hooksPath not set"; exit 1; }

echo "== kern setup --dry-run (read-only preview)"
"$KERN" setup --dry-run
extra=$(repo_extras)
test -z "$extra" || { echo "FAIL: setup --dry-run wrote repo files: $extra"; exit 1; }

echo "== git hooks: fail open without kern, refuse force-push to main"
git remote add origin "$WORK/remote.git"
git add -A
commit -m one
git push -q origin main
echo "// two" >> a.go
commit -am two
# Push "two" so the remote actually holds the diverged history: without
# this, the later push of "three" is a fast-forward (remote still at
# "one") and the pre-push hook correctly allows it.
git push -q origin main
git reset -q --hard HEAD~1
echo "// three" >> a.go
commit -am three
if git push -q --force origin main 2>/dev/null; then
  echo "FAIL: force push to main was not blocked by the global pre-push hook"; exit 1
fi
KERN_BYPASS=1 KERN_BYPASS_REASON=ci-smoke git push -q --force origin main
test -s "$HOME/.kern/audit/bypass.jsonl" || { echo "FAIL: bypass was not audited"; exit 1; }

echo "== remove"
"$KERN" setup --remove --yes
test ! -e "$HOME/.kern/hooks/kern-guard.sh" || { echo "FAIL: guard script left behind"; exit 1; }
test -z "$(git config --global core.hooksPath || true)" || { echo "FAIL: core.hooksPath still set"; exit 1; }

echo "OK: global install smoke passed"
