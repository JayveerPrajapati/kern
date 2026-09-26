# Cold-Start Scale Benchmarks

**Date:** 2026-09-26
**Revision:** `7d8c5d5` (`git rev-parse --short HEAD` at measurement time)
**Suite:** cold-start-scale · **Status:** baseline established 2026-09-26

## 1. Purpose

The skeptic's core question, from the 2026-09-22 senior-engineer audit:
*"grep answered the same query in 355ms while kern took 456ms — the
deterministic index is slower than grep for the common case."* That
comparison was a single-shot measurement under ad-hoc conditions. This
suite measures the one-shot cold-start cost of `kern search` **across
repo scales** with a same-tree protocol, so the "vs grep" question gets
an honest, reproducible answer at every size a user actually has:

1. **cold-build** — no index exists; one `kern search` invocation pays
   walk + parse + graph build + persist + query. The worst case, paid
   once per repo (and after `kern index --force`).
2. **cold-load** — the persisted `.kern` index exists; a fresh process
   loads it and answers. The realistic one-shot CLI cost, and the class
   the original 456ms figure belonged to.
3. **grep baseline** — `grep -rn <term>` over the same tree. The
   incumbent every agent already has.

## 2. Methodology

### 2.1 The ladder

Five real repositories, copied to `/tmp` per run (never measured
in place):

| Rung | Profile | Files (scanned) | ~LOC | Languages |
|---|---|---|---|---|
| **tiny** | front-end tutorial collection | 83 | ~2.7k | JS/HTML/CSS |
| **small** | web app (Go backend + TS frontend) | 189 | ~22k | Go/TS/TSX |
| **medium** | **this kern repo** (the public anchor) | 1,806 | ~352k | Go |
| **big1** | Python service, venv-heavy | **64** (see §4) | ~2k real | Python |
| **big2** | enterprise backend | 7,819 | ~2.2M | Python/pyi |

big1's file count is the story, not a mistake — see §4.

### 2.2 Same-tree protocol (the fairness rule)

Both tools scan **byte-identical trees**. Each copy is stripped of
directories kern's index walk ignores (its `ignoreDirs` list): `.git`,
`node_modules`, `vendor`, `dist`, `build`, `out`, `target`, `.next`,
`__pycache__`, `.venv`, `.cache`, `.idea`, `bin`, `.mvn`, `coverage`,
`tmp`, `.kern`, agent-config dirs, `graphify-out`. This deliberately
removes grep's biggest unfair disadvantage (scanning dependency trees
nobody searches) while changing nothing kern does — it ignores those
directories by design. Without this rule the comparison measures
directory hygiene, not search.

### 2.3 Measurements

- **cold-build**: `rm -rf .kern` → time `kern search <term> <root>`
  (index build + persist + query in one process).
- **cold-load**: `.kern` present → time `kern search <term> <root>`
  (process start + index load + query).
- **grep**: time `grep -rn <term> <root>` (full text scan of every file).

Timing: python `subprocess` wrapper (wall clock, ms). 3 runs for
tiny/small/medium, 2 for big1/big2; tables show the **median** and the
run spread. Queries are terms that exist as symbols in each repo
(tiny: `App`, small: `main`, medium: `dispatch`, big1: `check`,
big2: `main`).

**Result shapes differ — this is not apples-to-apples output.** kern
returns the top-20 typed symbols (kind, language, file:line); grep
returns *every* raw matching line (517 lines at medium, 5,449 at big2).
Timing compares time-to-answer, not result volume.

### 2.4 Environment

- **Machine:** Apple M1 Pro, 16 GB RAM, macOS 26.7 (darwin/arm64)
- **Binary:** `kern` built from `7d8c5d5` (installed via `make install`)
- **grep:** BSD grep (macOS system)
- Copies on local SSD; sequential runs; no noise control beyond
  back-to-back repetition.

### 2.5 Reproducing

The measurement script (copy → strip → N timed runs per measurement) is
reproduced in §6. Median of the runs is the reported number.

## 3. Results

| Rung | Files | cold-build (median) | cold-load (median) | grep -rn (median) | `.kern` footprint |
|---|---|---|---|---|---|
| tiny | 83 | 92 ms | 86 ms | **70 ms** | 408 KB / 3.6 MB tree |
| small | 189 | 456 ms | **134 ms** | 496 ms | 1.5 MB / 47 MB |
| medium | 1,806 | 2,175 ms | **575 ms** | 1,592 ms | 20 MB / 107 MB |
| big2 | 7,819 | 11,816 ms | **872 ms** | 5,799 ms | 43 MB / 351 MB |

Run spread (min–max): tiny build 92–103, load 82–88, grep 66–70 ·
small build 449–457, load 129–138, grep 495–501 · medium build
2,170–2,238, load 565–578, grep 1,559–1,820 · big2 build 11,802–11,830,
load 862–882, grep 5,547–6,051.

## 4. The venv-heavy repo (big1) — why file counts mislead

big1 is 149 MB on disk and 2,100+ `.py` files by naive `find` — but 99%
of that is `.venv` site-packages and caches. After the §2.2 strip it is
**64 source files**. On the stripped tree: cold-build ~185 ms,
cold-load ~100 ms, grep ~43 ms (tiny-scale behavior, as expected).
The inverse point matters for the skeptic: raw `grep -rn` on the
*unstripped* tree would spend nearly all its time scanning dependency
code a user never wrote — while kern's walk ignores it by design. The
same-tree protocol is what makes the comparison meaningful.

## 5. Verdict

- **Tiny repos: grep wins.** kern's process + index machinery (~90 ms)
  exceeds the whole job. If your repo fits in one screen, grep.
- **Small repos: kern cold-load wins ~3.7×** (134 vs 496 ms); the
  first-ever build (456 ms) costs about one grep pass.
- **Medium (this repo, 1.8k files): cold-load wins 2.8×** (575 vs
  1,592 ms); cold-build (2.2 s) loses to a single grep pass by ~1.4×,
  but it is paid once and amortized by the resident MCP server —
  per-call cost after startup is query-only.
- **Big (7.8k files, 2.2M LOC): cold-load wins 6.6×** (872 vs 5,799 ms);
  cold-build (11.8 s) is ~2× one grep pass, again once per repo.
- **The original 456ms-vs-355ms figure is superseded by this protocol.**
  Same-tree copies, medians over repeated runs, and per-repo scale show
  the real shape: grep is fastest only where the job is trivially small;
  from ~200 files up, a loaded kern index answers several times faster
  than a full grep scan, and the build cost amortizes over the resident
  server's lifetime.
- **Honest costs on our side:** the index costs disk (11–19% of tree
  size), the first build is slower than one grep pass, and a stale
  index serves stale answers (mitigated by the inline STALE note and
  automatic rebuild). Typed top-20 symbols vs raw lines is a different
  product, not a strict superset.

## 6. Reproduction script

```sh
#!/bin/bash
# cold-start ladder — same-tree protocol (§2.2), medians over N runs
KERN=~/.local/bin/kern
py() { python3 -c '
import subprocess, sys, time
t=time.time(); p=subprocess.run(sys.argv[1:], capture_output=True, text=True)
print(f"{(time.time()-t)*1000:.0f}ms exit={p.returncode}")' "$@"; }
strip() { find "$1" \( -name .git -o -name node_modules -o -name vendor \
  -o -name dist -o -name build -o -name out -o -name target -o -name .next \
  -o -name __pycache__ -o -name .venv -o -name .cache -o -name .idea \
  -o -name bin -o -name .mvn -o -name coverage -o -name tmp -o -name .kern \
  -o -name .opencode \) -type d -prune -exec rm -rf {} +; }
bench() { # name src tries term...
  local name=$1 src=$2 tries=$3 term=$4 dst=/tmp/kbench/$name
  rm -rf "$dst"; cp -R "$src" "$dst"; strip "$dst"
  for i in $(seq $tries); do rm -rf "$dst/.kern"; py $KERN search "$term" "$dst"; done
  for i in $(seq $tries); do py $KERN search "$term" "$dst"; done
  for i in $(seq $tries); do py grep -rn "$term" "$dst"; done
  du -sh "$dst/.kern"
}
bench tiny  ~/repos/frontend-collection 3 App
bench small ~/repos/webapp             3 main
bench medium ~/repos/kern              3 dispatch
bench big2  ~/repos/enterprise-backend 2 main
```
