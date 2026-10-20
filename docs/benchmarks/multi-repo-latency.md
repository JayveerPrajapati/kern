# Multi-Repo Latency Benchmarks (Measured)

**Date:** 2026-10-17
**Revision:** `19e4370` (kern binary built from this repo at measurement time)
**Suite:** multi-repo-latency · **Status:** baseline established 2026-10-17 ·
**Provenance:** [measured] — every number below was produced by a live
`kern bench` run against a real working tree on the disclosed machine.

## 1. Purpose

kern's published latency story was fixture-scale: the graph-latency suite
measures a synthetic 12k-symbol corpus, and cold-start measures same-tree
synthetic scale ladders. This suite answers the obvious follow-up — "what
happens on real repos you actually work in?" — with measured numbers from
four real codebases, one of them kern's own monorepo.

It also records what the first measurement round FOUND: two real indexer
bugs that refused to index two of the four repos outright (§4). A benchmark
protocol that only ever runs on fixtures cannot find those.

## 2. Methodology

Command (identical for every repo, regenerates the whole table):

```sh
kern bench --root <repo> --json
```

- **Binary:** built from this repo at git `19e4370` (sqlite + tree-sitter
  build). Same binary for all four repos, one session, back-to-back.
- **Machine:** darwin/arm64, 8 CPUs, go1.27.1 (Apple Silicon laptop, AC
  power, no other heavy processes). Single-machine numbers — they are NOT
  cross-machine comparable; compare the SHAPE (warm/cold ratio, query
  scaling with symbol count), not absolute milliseconds.
- **Protocol** (per repo, fixed by the harness): 1 fresh cold index build
  (cache wiped), 3 warm index loads, 5 runs per query; the table shows
  median (min). The query target is picked by the harness per repo
  (`main`, `len`, `fmt.Errorf`, `AsyncMock` — each present in that repo's
  index), so query numbers compare kernels, not identical targets.
- **Repos:** A is this repository (public). B–D are private production
  codebases, anonymized by label with their shape disclosed.

## 3. Measured results ([measured], 2026-10-17, machine above)

| repo | shape | symbols | cold build | warm load | speedup | search | ranked | callers | blast | hubs |
|---|---|---|---|---|---|---|---|---|---|---|
| A: kern (this repo) | Go monorepo, ~200 packages | 20316 | 1051.8 ms | 218.7 ms | 4.8x | 893.3 µs | 13.94 ms | 42 ns | 17.98 ms | 111.5 ms |
| B: trading platform | Go backend + TSX frontend + hand-written CSS | 3469 | 1054.5 ms | 23.0 ms | 45.8x | 115.5 µs | 835.3 µs | 42 ns | 923.1 µs | 18.2 ms |
| C: services monorepo | Go services + TSX frontend | 2617 | 361.0 ms | 16.5 ms | 21.9x | 91.8 µs | 763.0 µs | 41 ns | 356.1 µs | 12.0 ms |
| D: log checker | Python service + shell tooling | 979 | 326.0 ms | 11.6 ms | 28.0x | 62.4 µs | 669.5 µs | 42 ns | 531.2 µs | 3.2 ms |

Honest read:

- One-hop callers is nanoseconds everywhere — the "sub-millisecond
  graph" claim holds on real repos at these scales.
- Warm load stays 12–219 ms while cold build is 0.3–1.1 s: the store
  pays for itself within one session on every repo measured.
- Hub ranking is the only query that scales painfully with symbol
  count (111.5 ms at 20k symbols vs 3.2 ms at 1k) — consistent with the
  graph-latency suite's finding at synthetic scale.
- Repo B's cold build (1.05 s for 3.5k symbols) is dominated by TSX/CSS
  tree-sitter parsing, not symbol count: repo A builds 20316 symbols in
  the same wall time on the Go-heavy mix.

## 4. What the first round found (and fixed)

The first measurement round REFUSED to index repos B and C — the
fail-loud duplicate-symbol gate fired on valid code, which is exactly
what a multi-repo protocol is for:

1. **TSX arrow parameters extracted as phantom methods.** An anonymous
   arrow function has no name of its own; the extractor's child scan
   returned the first PARAMETER name (`n` in `xs.map(n => n * 2)`), and
   two callbacks sharing a parameter name inside one enclosing function
   collided on the gate. Fixed: arrow functions are no longer symbol
   nodes (named bindings keep their symbol via the const-declaration
   promotion). Regression test:
   `internal/index/treesitter_test.go TestTreeSitterAnonymousArrowParamsNotSymbols`.
2. **CSS re-selection extracted as duplicate classes.** `.btn:disabled`,
   `.data-table th` and media-query re-selections are how CSS is
   written; the extractor minted a duplicate class symbol per
   re-selection and the gate refused the file. Fixed: CSS keeps the
   first occurrence per (kind, name) — the definition site — in BOTH
   extractors (tree-sitter and the `-tags notreesitter` regex path), so
   parity holds. Regression tests:
   `internal/index/treesitter_test.go TestTreeSitterCSSReselectionDeduped`,
   `internal/langparse/foreign_test.go TestCssReselectionDedupedRegex`.

Known pre-existing (found by running the notreesitter build of this
suite's package tests, recorded here so it is not re-discovered):
`go test -tags notreesitter ./internal/langparse/` fails
`TestJavascriptExtract`/`TestRustExtract` on HEAD — the regex extractor
does not surface JS class-method / Rust impl-method symbols those tests
expect. Out of scope for this suite; filed for the next campaign.

## 5. Regenerating

Re-run the command from §2 against the same repos (or your own) after
any indexer or store change and update the table together with the
regression tests above. Numbers are only meaningful against the same
machine/revision disclosure — never copy them without it.
