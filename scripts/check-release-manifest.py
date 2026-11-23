#!/usr/bin/env python3
"""Fail closed when release metadata drifts from the shipped product."""

import argparse
import json
import re
import sys
from pathlib import Path


EXPECTED_COMMAND_COUNT = 76
EXPECTED_MCP_TOOL_COUNT = 34
EXPECTED_DEFAULT_MCP_COUNT = 11
EXPECTED_DEFAULT_TOOLS = {
    "kern_buddy",
    "kern_search",
    "kern_explore",
    "kern_context",
    "kern_probe",
    "kern_research",
    "kern_impact",
    "kern_graph",
    "kern_review",
    "kern_verify",
    "kern_meta",
}


def fail(message):
    print("release-manifest: FAIL: {}".format(message), file=sys.stderr)
    raise SystemExit(1)


def normalize_version(value):
    return value[1:] if value.startswith("v") else value


def read(path):
    try:
        return path.read_text(encoding="utf-8")
    except OSError as exc:
        fail("cannot read {}: {}".format(path, exc))


def version_tuple(value):
    try:
        return tuple(int(part) for part in value.split("."))
    except ValueError:
        fail("invalid version {!r}".format(value))


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--root", default=".", help="repository root")
    parser.add_argument("--tag", help="expected release tag (for a real release)")
    parser.add_argument("--brew-formula", help="generated Homebrew formula to check against --tag")
    args = parser.parse_args()
    root = Path(args.root).resolve()

    server_path = root / "server.json"
    try:
        server = json.loads(server_path.read_text(encoding="utf-8"))
    except (OSError, ValueError) as exc:
        fail("cannot parse {}: {}".format(server_path, exc))

    changelog = read(root / "CHANGELOG.md")
    match = re.search(r"^## \[([0-9]+(?:\.[0-9]+)+)\]", changelog, re.MULTILINE)
    if not match:
        fail("CHANGELOG.md has no versioned release heading")
    changelog_version = match.group(1)
    server_version = str(server.get("version", ""))
    if not server_version:
        fail("server.json has no version")
    if normalize_version(server_version) != changelog_version:
        fail(
            "server.json version {} does not match CHANGELOG.md latest version {}".format(
                server_version, changelog_version
            )
        )
    if args.tag and normalize_version(args.tag) != changelog_version:
        fail(
            "release tag {} does not match CHANGELOG.md latest version {}".format(
                args.tag, changelog_version
            )
        )

    go_mod = read(root / "go.mod")
    go_match = re.search(r"^go ([0-9]+(?:\.[0-9]+)+)", go_mod, re.MULTILINE)
    if not go_match:
        fail("go.mod has no Go version directive")
    go_version = go_match.group(1)

    brew_path = root / "homebrew" / "kern.rb"
    brew = read(brew_path)
    brew_match = re.search(
        r'ENV\["HOMEBREW_GO_VERSION"\]\s*=\s*"([0-9]+(?:\.[0-9]+)+)"', brew
    )
    if not brew_match:
        fail("homebrew/kern.rb does not pin HOMEBREW_GO_VERSION")
    brew_go_version = brew_match.group(1)
    if version_tuple(brew_go_version) < version_tuple(go_version):
        fail(
            "Homebrew Go {} is older than go.mod requirement {}".format(
                brew_go_version, go_version
            )
        )
    for source in ("./cmd/kern", "./cmd/kern-mcp"):
        if source not in brew:
            fail("Homebrew formula does not build {}".format(source))

    if args.brew_formula:
        formula = read(Path(args.brew_formula))
        expected_tag = args.tag if args.tag else "v" + changelog_version
        if 'version "{}"'.format(expected_tag) not in formula:
            fail("generated Homebrew formula is not stamped as {}".format(expected_tag))

    dispatch = read(root / "cmd" / "kern" / "dispatch_table.go")
    table_start = dispatch.find("var commandTable = map[string]commandEntry{")
    table_end = dispatch.find("\n}\n\nvar commandFlags", table_start)
    if table_start < 0 or table_end < 0:
        fail("cmd/kern/dispatch_table.go does not contain a parseable commandTable")
    command_table = dispatch[table_start:table_end]
    command_names = re.findall(
        r'^\s*"([^"]+)":\s*\{', command_table, re.MULTILINE
    )
    if len(command_names) != EXPECTED_COMMAND_COUNT:
        fail(
            "command table has {} entries, expected {}".format(
                len(command_names), EXPECTED_COMMAND_COUNT
            )
        )
    if len(set(command_names)) != len(command_names):
        fail("command table contains duplicate command names")

    catalog = read(root / "internal" / "mcpcatalog" / "tools.go")
    tool_names = re.findall(r'Name:\s*"(kern_[a-z0-9_]+)"', catalog)
    if len(tool_names) != EXPECTED_MCP_TOOL_COUNT:
        fail(
            "MCP catalog has {} tools, expected {}".format(
                len(tool_names), EXPECTED_MCP_TOOL_COUNT
            )
        )
    if len(set(tool_names)) != len(tool_names):
        fail("MCP catalog contains duplicate tool names")

    surface = read(root / "internal" / "mcp" / "toolsurface" / "toolsurface.go")
    default_tools = set(re.findall(r'"(kern_[a-z0-9_]+)",', surface))
    if len(default_tools) != EXPECTED_DEFAULT_MCP_COUNT:
        fail(
            "default MCP surface has {} tools, expected {}".format(
                len(default_tools), EXPECTED_DEFAULT_MCP_COUNT
            )
        )
    if default_tools != EXPECTED_DEFAULT_TOOLS:
        missing = sorted(EXPECTED_DEFAULT_TOOLS - default_tools)
        extra = sorted(default_tools - EXPECTED_DEFAULT_TOOLS)
        fail(
            "default MCP surface drifted (missing={}, extra={})".format(
                ",".join(missing), ",".join(extra)
            )
        )
    if not default_tools.issubset(set(tool_names)):
        fail("default MCP surface contains a tool absent from the catalog")
    if "kern_arch" in default_tools:
        fail("kern_arch is not part of the default MCP surface")

    python_readme = read(root / "python" / "README.md")
    if "Pin a `v`-prefixed release tag" not in python_readme:
        fail("python/README.md does not document a v-prefixed KERN_VERSION tag")
    if "requires Go 1.27+" not in python_readme:
        fail("python/README.md does not document Go 1.27+")

    print(
        "release-manifest: PASS — version {}, Go {} / {}, commands {}, MCP {}/{}".format(
            server_version,
            go_version,
            brew_go_version,
            len(command_names),
            len(default_tools),
            len(tool_names),
        )
    )


if __name__ == "__main__":
    main()
