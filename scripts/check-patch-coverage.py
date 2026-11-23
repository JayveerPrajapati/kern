#!/usr/bin/env python3
"""Enforce line coverage for Go lines added since a base revision.

This is the local, workflow-enforceable counterpart to Codecov's patch status.
It deliberately measures executable changed lines: a line is executable only
when it falls inside a Go coverage block. Comments, blank lines, and package
declarations therefore do not inflate the denominator.
"""

from __future__ import annotations

import argparse
import re
import subprocess
import sys
from collections import defaultdict
from pathlib import Path


HUNK_RE = re.compile(r"^@@.*\+(?P<start>\d+)(?:,(?P<count>\d+))?")
BLOCK_RE = re.compile(r"^(?P<file>.+):(?P<start>\d+)\.\d+,(?P<end>\d+)\.\d+\s+\d+\s+(?P<count>\d+)$")


def added_go_lines(base: str, root: Path) -> dict[str, set[int]]:
    diff = subprocess.run(shell=False,
        args=["git", "diff", "--unified=0", "--no-ext-diff", base, "--", "*.go"],
        cwd=root,
        check=True,
        text=True,
        stdout=subprocess.PIPE,
    ).stdout
    changed: dict[str, set[int]] = defaultdict(set)
    current_file: str | None = None
    next_line = 0

    for line in diff.splitlines():
        if line.startswith("+++ b/"):
            current_file = line[len("+++ b/") :]
            next_line = 0
            continue
        match = HUNK_RE.match(line)
        if match:
            count = int(match.group("count") or "1")
            next_line = int(match.group("start")) if count else 0
            continue
        if current_file is None or next_line == 0:
            continue
        if line.startswith("+"):
            changed[current_file].add(next_line)
            next_line += 1

    return changed


def coverage_blocks(profile: Path) -> dict[str, list[tuple[int, int, int]]]:
    blocks: dict[str, list[tuple[int, int, int]]] = defaultdict(list)
    with profile.open(encoding="utf-8") as handle:
        for line in handle:
            match = BLOCK_RE.match(line.rstrip("\n"))
            if match:
                blocks[match.group("file")].append(
                    (
                        int(match.group("start")),
                        int(match.group("end")),
                        int(match.group("count")),
                    )
                )
    return blocks


def normalize_path(path: str, root: Path) -> str:
    return path.removeprefix(f"{root.name}/")


def patch_coverage(
    changed: dict[str, set[int]], blocks: dict[str, list[tuple[int, int, int]]], root: Path
) -> tuple[int, int, list[tuple[str, int]]]:
    executable = 0
    covered = 0
    missed: list[tuple[str, int]] = []
    by_suffix: dict[str, list[tuple[int, int, int]]] = {}
    for file, file_blocks in blocks.items():
        by_suffix[file.split("/")[-1]] = file_blocks

    for file, lines in changed.items():
        file_blocks = blocks.get(normalize_path(file, root))
        if file_blocks is None:
            file_blocks = by_suffix.get(Path(file).name, [])
        if not file_blocks:
            continue
        for line in lines:
            if any(start <= line <= end for start, end, _ in file_blocks):
                executable += 1
                if any(start <= line <= end and count > 0 for start, end, count in file_blocks):
                    covered += 1
                else:
                    missed.append((file, line))

    return executable, covered, missed


def main() -> int:
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--profile", default="coverage.out")
    parser.add_argument("--base", required=True, help="commit, SHA, or ref to compare against")
    parser.add_argument("--fail-under", type=float, default=75.0)
    args = parser.parse_args()

    root = Path.cwd()
    profile = root / args.profile
    if not profile.is_file():
        print(f"patch-coverage: missing profile {profile}", file=sys.stderr)
        return 1

    changed = added_go_lines(args.base, root)
    executable, covered, missed = patch_coverage(changed, coverage_blocks(profile), root)
    percentage = 100.0 * covered / executable if executable else 100.0
    print(f"patch-coverage: {percentage:.2f}% ({covered}/{executable} executable changed Go lines)")
    if percentage < args.fail_under:
        print(f"patch-coverage: FAIL {percentage:.2f}% < {args.fail_under:.2f}%", file=sys.stderr)
        for file, line in missed:
            print(f"  uncovered: {file}:{line}", file=sys.stderr)
        return 1
    print(f"patch-coverage: PASS {percentage:.2f}% >= {args.fail_under:.2f}%")
    return 0


if __name__ == "__main__":
    raise SystemExit(main())
