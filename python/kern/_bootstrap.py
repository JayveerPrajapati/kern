# kern pip shim — thin wrapper that fetches the prebuilt Go binary on first
# use, caches it, and execs it. The real kern is a single static binary; this
# package only makes it installable via pip for people who prefer that.
#
# Distribution note: replace JayveerPrajapati with your GitHub username.

import hashlib
import os
import platform
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
    # platform is portable; os.uname() does not exist on Windows.
    sysname = platform.system().lower()
    machine = platform.machine().lower()
    arch = {"x86_64": "amd64", "amd64": "amd64", "aarch64": "arm64", "arm64": "arm64"}.get(
        machine
    )
    if sysname not in ("linux", "darwin") or arch is None:
        return None
    return "{}-{}".format(sysname, arch)


def _safe_extract(tar, dest):
    """Extract an untrusted archive without letting members escape dest."""
    base = os.path.normpath(dest)
    for member in tar.getmembers():
        target = os.path.normpath(os.path.join(base, member.name))
        if target != base and not target.startswith(base + os.sep):
            raise tarfile.TarError(
                "unsafe member in archive: {!r}".format(member.name)
            )
    try:  # Python 3.12+: the data filter blocks absolute/.. members too
        tar.extractall(dest, filter="data")
    except TypeError:  # Python < 3.12 has no filter argument
        tar.extractall(dest)


def _download(url, dest, timeout=60):
    """Download url to dest with a timeout (urlretrieve has no timeout knob)."""
    req = urllib.request.Request(url, headers={"User-Agent": "kern-pip"})
    with urllib.request.urlopen(req, timeout=timeout) as r:
        with open(dest, "wb") as f:
            shutil.copyfileobj(r, f)


def _verify_sha256(tarball, tag):
    """Verify the tarball against the release SHA256SUMS if present.
    install.sh already does this; the pip shim must not skip it (W2-49)."""
    sums_url = "https://github.com/{}/releases/download/{}/{}".format(
        REPO, tag, "SHA256SUMS"
    )
    try:
        req = urllib.request.Request(sums_url, headers={"User-Agent": "kern-pip"})
        with urllib.request.urlopen(req, timeout=20) as r:
            sums = r.read().decode("utf-8", errors="replace")
    except Exception:
        # No SHA256SUMS is non-fatal (older/human releases); skip silently.
        return None
    name = os.path.basename(tarball)
    for line in sums.splitlines():
        parts = line.split()
        if len(parts) == 2 and parts[1] == name:
            return parts[0].lower()
    return None


def _fetch(tag, os_arch):
    file = "kern-{}.tar.gz".format(os_arch)
    url = "https://github.com/{}/releases/download/{}/{}".format(REPO, tag, file)
    with tempfile.TemporaryDirectory() as tmp:
        tarball = os.path.join(tmp, file)
        print("kern: downloading {} ({})".format(tag, file), file=sys.stderr)
        _download(url, tarball)
        want = _verify_sha256(tarball, tag)
        if want:
            got = hashlib.sha256(open(tarball, "rb").read()).hexdigest()
            if got != want:
                raise SystemExit(
                    "kern: SHA256 mismatch for {}: want {}, got {}".format(file, want, got)
                )
        with tarfile.open(tarball, "r:gz") as t:
            _safe_extract(t, tmp)
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
