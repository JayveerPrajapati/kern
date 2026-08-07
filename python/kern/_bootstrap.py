# kern pip shim — thin wrapper that fetches the prebuilt Go binary on first
# use, caches it, and execs it. The real kern is a single static binary; this
# package only makes it installable via pip for people who prefer that.
#
# Distribution note: replace JayveerPrajapati with your GitHub username.

import os
import shutil
import subprocess
import sys
import tarfile
import tempfile
import urllib.request

try:  # Python 3.8+
    from importlib import metadata as importlib_metadata
except ImportError:
    importlib_metadata = None

def _package_version():
    if importlib_metadata is not None:
        try:
            return importlib_metadata.version("kern-context")
        except Exception:
            pass
    return "unknown"

__version__ = _package_version()

REPO = os.environ.get("KERN_REPO", "JayveerPrajapati/kern")
VERSION = os.environ.get("KERN_VERSION", "latest")


def _cache_dir():
    base = os.environ.get("XDG_CACHE_HOME") or os.path.join(
        os.path.expanduser("~"), ".cache"
    )
    return os.path.join(base, "kern-pip")


def _binary_path():
    return os.path.join(_cache_dir(), "bin", "kern")


def _marker_path():
    return os.path.join(_cache_dir(), "bin", ".kern-version")


def _resolve_version():
    if VERSION != "latest":
        return VERSION
    url = "https://api.github.com/repos/{}/releases/latest".format(REPO)
    try:
        with urllib.request.urlopen(url, timeout=20) as r:
            data = r.read()
        import json

        return json.loads(data)["tag_name"]
    except Exception as exc:
        # The GitHub releases/latest endpoint is unauthenticated (60 req/hr) and
        # returns nothing before the first release exists. Fail with an
        # actionable message instead of a raw traceback.
        raise SystemExit(
            "kern: could not resolve the latest release from GitHub ({}); "
            "this usually means the API is rate-limited or no release exists yet.\n"
            "Install Go and run:\n"
            "  go install github.com/{}/cmd/kern@latest".format(exc, REPO)
        )


def _os_arch():
    uname = os.uname()
    sysname = uname.sysname.lower()
    machine = uname.machine.lower()
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(
        machine
    )
    if sysname not in ("linux", "darwin") or arch is None:
        return None
    return "{}-{}".format(sysname, arch)


def _fetch(tag, os_arch):
    file = "kern-{}.tar.gz".format(os_arch)
    url = "https://github.com/{}/releases/download/{}/{}".format(REPO, tag, file)
    with tempfile.TemporaryDirectory() as tmp:
        tarball = os.path.join(tmp, file)
        print("kern: downloading {} ({})".format(tag, file), file=sys.stderr)
        urllib.request.urlretrieve(url, tarball)
        with tarfile.open(tarball, "r:gz") as t:
            t.extractall(tmp)
        bin_dir = os.path.join(_cache_dir(), "bin")
        os.makedirs(bin_dir, exist_ok=True)
        for name in ("kern", "kern-mcp"):
            src = os.path.join(tmp, "kern-{}".format(os_arch), name)
            dst = os.path.join(bin_dir, name)
            shutil.copy2(src, dst)
            os.chmod(dst, 0o755)
        with open(_marker_path(), "w") as f:
            f.write(tag)


def _cached_version():
    try:
        with open(_marker_path()) as f:
            return f.read().strip()
    except OSError:
        return None


def main():
    binary = _binary_path()
    need = not os.path.exists(binary)
    if not need and VERSION != "latest":
        need = _cached_version() != VERSION
    if need:
        target = _os_arch()
        if target is None:
            raise SystemExit(
                "kern: no prebuilt binary for this platform; install Go and use "
                "'go install github.com/{}/cmd/kern@latest'".format(REPO)
            )
        tag = _resolve_version()
        try:
            _fetch(tag, target)
        except Exception as exc:  # noqa: BLE001
            raise SystemExit(
                "kern: failed to download release: {}\nInstall Go and run:\n"
                "  go install github.com/{}/cmd/kern@latest".format(exc, REPO)
            )
    os.execv(binary, [binary] + sys.argv[1:])


if __name__ == "__main__":
    main()
