# Self-healing Blueprint pre-commit adapter (PowerShell version).
#
# Prunes stale entries from .blueprint/fingerprint-cache/fingerprints.json
# before running `kern check --staged --format=terminal`.

[CmdletBinding()]
param(
    [Parameter(ValueFromRemainingArguments = $true)]
    [string[]]$KernArgs
)

$ErrorActionPreference = "Stop"

$root = (git rev-parse --show-toplevel 2>$null)
if (-not $root) {
    $root = "."
}

$cache = Join-Path $root ".blueprint/fingerprint-cache/fingerprints.json"

if (Test-Path $cache) {
    if (Get-Command python3 -ErrorAction SilentlyContinue) {
        $pyCode = @"
import hashlib, json, os, sys, tempfile
path, root = sys.argv[1], os.path.normpath(sys.argv[2])
try:
    with open(path, encoding="utf-8") as f:
        data = json.load(f)
except (OSError, ValueError):
    if os.path.exists(path):
        os.replace(path, path + ".corrupt-" + str(os.getpid()))
    sys.exit(0)

files = data.get("files", {})
if not files:
    sys.exit(0)

changed = []
for rel, entry in list(files.items()):
    abspath = os.path.normpath(os.path.join(root, rel))
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

sys.stderr.write("kern check: pruned %d stale fingerprint entries (%s)\n"
                 % (len(changed), ", ".join(changed)))
"@
        python3 -c $pyCode "$cache" "$root"
    }
}

& kern check --staged --format=terminal $KernArgs
exit $LASTEXITCODE
