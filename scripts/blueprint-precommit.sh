#!/bin/sh
# Self-healing Blueprint pre-commit adapter.
#
# Blueprint's fingerprint cache (.blueprint/fingerprint-cache/fingerprints.json)
# goes stale on the first run after files change (edits, refactors, dedup):
# the cached fingerprints for the touched files no longer match their
# content_hash, and `blueprint check --staged` can false-block on the stale
# data — the G-11 "hook protocol" quirk that previously forced a manual
# `git commit --amend --no-edit` ritual to re-run the check on a fresh cache.
#
# This adapter prunes stale entries (sha256 mismatch or vanished file) BEFORE
# invoking blueprint, so every run starts from a fresh cache for exactly the
# files that changed. Behavior otherwise matches the thin adapter installed by
# `blueprint install hook`: exit code is blueprint's own (0 PASS, 1 BLOCK,
# 2 tool error, 3 config, 4 unsupported).
#
# Usage: scripts/blueprint-precommit.sh [blueprint args...]
# Install as the pre-commit hook (or have the hook exec this script).

set -u

root="$(git rev-parse --show-toplevel 2>/dev/null || echo .)"
cache="$root/.blueprint/fingerprint-cache/fingerprints.json"

if [ -f "$cache" ]; then
  if command -v python3 >/dev/null 2>&1; then
    python3 - "$cache" "$root" << 'PYEOF' || true
import hashlib, json, os, sys, tempfile

path, root = sys.argv[1], os.path.normpath(sys.argv[2])
try:
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
except (OSError, ValueError):
    # Unreadable or corrupt cache: back it up and let blueprint rebuild.
    if os.path.exists(path):
        os.replace(path, path + ".corrupt-" + str(os.getpid()))
    sys.exit(0)

files = data.get("files", {})
if not files:
    sys.exit(0)

changed = []
for rel, entry in list(files.items()):
    abspath = os.path.normpath(os.path.join(root, rel))
    # Only trust entries inside the repo; anything outside is anomalous.
    if os.path.commonpath([root, abspath]) != root:
        changed.append(rel)
        continue
    if not os.path.exists(abspath):
        changed.append(rel)
        continue
    try:
        with open(abspath, "rb") as f:
            digest = hashlib.sha256(f.read()).hexdigest()
    except OSError:
        changed.append(rel)
        continue
    if digest != entry.get("content_hash"):
        changed.append(rel)

if not changed:
    sys.exit(0)

for rel in changed:
    files.pop(rel, None)

fd, tmp = tempfile.mkstemp(dir=os.path.dirname(path), prefix="fingerprints.", suffix=".json")
try:
    with os.fdopen(fd, "w", encoding="utf-8") as f:
        json.dump(data, f, indent=2)
        f.write("\n")
    os.replace(tmp, path)
except BaseException:
    try:
        os.unlink(tmp)
    except OSError:
        pass
    raise

sys.stderr.write("blueprint: pruned %d stale fingerprint entries (%s)\n"
                 % (len(changed), ", ".join(changed)))
PYEOF
  fi
fi

exec blueprint check --staged --format=terminal "$@"
