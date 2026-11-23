#!/usr/bin/env python3
"""Fail when user-facing architecture claims drift from the live tree."""

from __future__ import annotations

import re
import subprocess
import sys
from pathlib import Path


def fail(message: str) -> None:
    print(f"docs-drift: FAIL: {message}", file=sys.stderr)
    raise SystemExit(1)


def read(path: Path) -> str:
    try:
        return path.read_text(encoding="utf-8")
    except OSError as exc:
        fail(f"cannot read {path}: {exc}")


def command_count(dispatch: str) -> int:
    start = dispatch.find("var commandTable = map[string]commandEntry{")
    end = dispatch.find("\n}\n\nvar commandFlags", start)
    if start < 0 or end < 0:
        fail("commandTable is not parseable")
    names = re.findall(r'^\s*"([^"]+)":\s*\{', dispatch[start:end], re.MULTILINE)
    if len(names) != len(set(names)):
        fail("commandTable contains duplicate names")
    return len(names)


def main() -> int:
    repo = Path(__file__).resolve().parents[1]
    dispatch = read(repo / "cmd" / "kern" / "dispatch_table.go")
    catalog = read(repo / "internal" / "mcpcatalog" / "tools.go")
    surface = read(repo / "internal" / "mcp" / "toolsurface" / "toolsurface.go")
    tour = read(repo / "docs" / "contributing" / "architecture-tour.md")
    readme = read(repo / "README.md")
    tool_catalog = read(repo / "docs" / "tool-catalog.md")

    commands = command_count(dispatch)
    tools = re.findall(r'Name:\s*"(kern_[a-z0-9_]+)"', catalog)
    default_tools = set(re.findall(r'"(kern_[a-z0-9_]+)",', surface))
    packages = sum(child.is_dir() and child.name != ".kern" for child in (repo / "internal").iterdir())
    binaries = sum((repo / "cmd" / name / "main.go").is_file() for name in ("kern", "kern-mcp"))

    expected = {
        "two binaries": "two binaries" in tour,
        "command count": f"{commands} commands" in tour,
        "package count": f"{packages} packages" in tour,
        "full MCP count": f"34-tool catalog" in tour,
        "default MCP count": "direct eleven-tool surface" in tour,
        "README full MCP count": f"{len(tools)} in full mode" in readme,
        "tool catalog count": f"{len(tools)} tools" in tool_catalog,
    }
    for name, ok in expected.items():
        if not ok:
            fail(f"{name} claim is stale or missing")
    if len(tools) != 34 or len(default_tools) != 11:
        fail(f"live MCP surface is {len(default_tools)}/{len(tools)}, expected 11/34")

    if len(readme.encode("utf-8")) > 12_000:
        fail("README.md is no longer the short user-facing entry point (limit: 12,000 bytes)")

    critical_paths = [
        "cmd/kern",
        "cmd/kern-mcp",
        "internal/mcp/toolsurface/toolsurface.go",
        "internal/mcpcatalog/tools.go",
        ".opencode/plugins/kern.ts",
        "internal/agentwire/assets/plugin/kern.ts",
        "internal/pluginhash/pluginhash.go",
        "docs/installation.md",
        "docs/benchmarks/overview.md",
        "docs/mcp/overview.md",
        "docs/security/overview.md",
        "docs/sdk/README.md",
    ]
    for relative in critical_paths:
        if not (repo / relative).exists():
            fail(f"documented path does not exist: {relative}")

    manifest = subprocess.run(shell=False,
        args=[sys.executable, str(repo / "scripts" / "check-release-manifest.py"), "--root", str(repo)],
        check=False,
    )
    if manifest.returncode:
        fail("release metadata gate failed")

    print(
        f"docs-drift: PASS — {binaries} binaries, {commands} commands, "
        f"{packages} packages, MCP {len(default_tools)}/{len(tools)}"
    )
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
