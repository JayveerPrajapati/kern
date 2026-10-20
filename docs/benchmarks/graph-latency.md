# Graph Query Latency Benchmarks

**Date:** 2026-10-19 (rerun; original baseline 2026-09-23)
**Revision:** `4783d41259eb911d7a2e49c14e2e4b48ed14b72c` (`git rev-parse HEAD` at measurement time)
**Suite:** graph-query-latency · **Status:** [measured] — numbers re-measured 2026-10-19 after the blast-radius caller-index fix (see §6)

## 1. Purpose

kern markets itself with the tagline **"sub-millisecond graph"** — that
"what depends on X" style queries against the code graph return in under a
millisecond. This suite exists to measure that claim honestly:

1. At the tiny fixture scale used by intel's unit tests (35 symbols).
2. At a realistic repo scale (~12k symbols, 240 files, 4 packages) where the
   graph actually has breadth and depth to walk.

The answer this document records: whether warm in-memory graph queries are
sub-millisecond, at which scale, and whether the marketing claim needs
qualification.

## 2. Methodology

### 2.1 Where the benchmarks live

All surviving benchmarks are in `internal/intel/bench_test.go` (package
`intel`), so they exercise the real query code paths (`BlastRadius`,
`prodCallersWithFileMap`) against a real `index.Index` built by
`index.Build` from real `.go` files written to a temp dir. The former hub-ranking
benchmarks (`BenchmarkGraphHubs`, `BenchmarkGraphHubsScaled`) were **deleted
from this package when the hubs family moved to `internal/topology`** — they
now live in `internal/topology/bench_split_test.go` (same fixtures,
`BenchmarkGraphHubs` / `BenchmarkGraphHubsScaled`), out of scope for this
page. The rows below reflect the three benchmarks that remain here.

### 2.2 Fixture shapes

| Fixture | Shape | Symbols |
|---|---|---|
| **small** (`benchFixture`) | 1 lib file + 12 app files; `lib.Public` called from every app file, 8 lib helpers, local chains | 35 |
| **scaled** (`benchFixtureScaled`) | 4 packages (`hub`, `svc1`, `svc2`, `svc3`) × 60 files; ~50 funcs/file; `hub.Public` called from every service file's root + every hub file; a depth-10 caller chain (`Chain0`..`Chain9`); each service package forms a dense intra-package call web (every 10th func calls cross-file) | **12,001** |

The scaled fixture is fully deterministic (every name derives from loop
indices) and its size is pinned by `assertScaledScale`, which fails the
benchmark if the symbol count ever leaves the 5k–15k documented range.

What the graph actually looks like for the queries being measured: the hub
symbol `Public` has ~1.7k direct callers (180 service-file roots + ~1.5k hub
package funcs + `Chain0`), and because every service func chains back to its
file's root, the **transitive** blast radius from `Public` covers all 12,001
symbols at depths up to 10.

### 2.3 What each benchmark measures

| Benchmark | Query under test | Cost model |
|---|---|---|
| `BenchmarkGraphQuery` | `BlastRadius(ix, ["Public"])` — transitive "what depends on X" (package-aware BFS over the reverse caller index) | small fixture |
| `BenchmarkGraphQueryScaled` | `BlastRadius(ix, ["Public"])` | scaled fixture, walks all 12k symbols |
| `BenchmarkDirectDependOnScaled` | `prodCallersWithFileMap(ix, "Public", fileMap)` — one-hop reverse-caller lookup (the cheapest dependency query) | scaled fixture |

The index (and for the direct benchmark the file map, which is
`O(Symbols)` — see `prodCallersWithFileMap`'s quadratic warning) is built
**outside** the timed loop in every benchmark, so `ns/op` measures the query
alone, not graph construction.

### 2.4 Reproducing

```sh
go test ./internal/intel -run '^$' -bench . -benchmem -benchtime 1s -count 5
```

Go reports, per run, the **mean** `ns/op` over that run's iterations; with
`-count 5` you get five such means. The tables below present the **median**
and **min** across those five runs (computed with the snippet in §2.5, since
`benchstat` was not available on this machine). Alloc counts (`B/op`,
`allocs/op`) were identical across all five runs for every benchmark.

Environment of this baseline:

- **Machine:** Apple M1 Pro, macOS (darwin/arm64)
- **Toolchain:** `go version go1.27.1 darwin/arm64`; module `go` directive `1.25.13`
- **No parallel benchmark noise control** (`-cpu` default); runs were
  sequential.

### 2.5 Min/median computation snippet

```python
import re, statistics
data = {}
for line in open('bench_out.txt'):
    m = re.match(r'^(Benchmark\S+?)-8\s+\d+\s+(\d+)\s+ns/op\s+(\d+)\s+B/op\s+(\d+)\s+allocs/op', line)
    if m:
        data.setdefault(m.group(1), []).append(int(m.group(2)))
for name, ns in data.items():
    print(name, 'median', statistics.median(ns), 'min', min(ns))
```

## 3. Results

Raw per-run output is in §5. Summary (median/min across 5 runs):

| Benchmark | Fixture | Symbols | Median ns/op | Min ns/op | Allocs/op | B/op |
|---|---|---|---|---|---|---|
| `BenchmarkGraphQuery` | small | 35 | **236,713 ns (0.237 ms)** | 236,458 ns | 3,079 | 169,890 |
| `BenchmarkGraphQueryScaled` | scaled | 12,001 | **144,733,393 ns (144.7 ms)** | 144,522,036 ns | 1,010,543 | 64,260,000 |
| `BenchmarkDirectDependOnScaled` | scaled | 12,001 | **372,468 ns (0.372 ms)** | 371,642 ns | 11 | 100,672 |

### 3.1 Reading the numbers

- **Small fixture:** the transitive query is ~0.24 ms — sub-millisecond on a
  35-symbol graph, though 30× the 2026-09-23 baseline (7.4 µs): the walk now
  resolves declaration identity per node (see §6).
- **Scaled fixture (12k symbols):**
  - One-hop direct lookup: **0.37 ms** — still sub-millisecond.
  - Transitive BlastRadius (walks the full 12k-symbol graph): **144.7 ms
    median** — ~145× over the 1 ms line. This is the honest cost of the
    current package-aware walk (per-node declaration resolution + caller
    attribution); the 2026-09-23 "3.14 ms" figure was measured before that
    resolution existed, and a 2026-10-19 regression pass measured the same
    query at **2.89 s** before the caller-index fix (see §6).
- **Memory scales with reach:** the transitive query allocates ~64 MB/op at
  12k symbols (1.0M allocs), down from ~1.7 GB / 1.96M allocs before the
  caller-index fix. The direct lookup allocates ~100 KB/op.

## 4. Verdict

**Is "sub-millisecond graph" honest?**

- **At small scale — yes.** On the 35-symbol fixture, warm transitive queries
  are ~0.24 ms and the one-hop lookup is a fraction of that. Well under a
  millisecond.
- **At realistic repo scale — only for direct lookups.** A one-hop "who calls
  X" query is ~0.37 ms (still sub-millisecond), but the headline *transitive*
  "what depends on X" query is **~145 ms median** on a 12,001-symbol /
  240-file / 4-package fixture. Hub ranking moved to `internal/topology`
  with the hubs family and is not re-measured on this page.
- **Cold vs warm:** all timings above are for a **warm in-memory index** —
  the index build itself is excluded from the timed loop. Building the
  12k-symbol index from scratch (fixture write + full `index.Build`) takes
  **~1.4 s** on this machine (measured as the wall time of a
  `-benchtime 1x` run minus the ~150 ms query).

**Bottom line:** "sub-millisecond graph" is honest **only** for warm
in-memory queries on small indexes and for direct one-hop lookups at any
scale. It needs qualification for realistic repos, where the transitive
query misses the 1 ms bar by ~145×, and where cold index builds are measured
in seconds. Suggested honest phrasing:

> *"Sub-millisecond warm in-memory graph queries at small scale; ~145 ms
> transitive queries on ~12k-symbol repos; cold index builds take ~1.4 s."*

## 5. Raw per-run output (5 × 1s runs, 2026-10-19)

```
goos: darwin
goarch: arm64
pkg: github.com/JayveerPrajapati/kern/internal/intel
cpu: Apple M1 Pro
BenchmarkGraphQuery-8             	    4752	    236458 ns/op	  169895 B/op	    3080 allocs/op
BenchmarkGraphQuery-8             	    5212	    236713 ns/op	  169891 B/op	    3079 allocs/op
BenchmarkGraphQuery-8             	    5114	    238118 ns/op	  169887 B/op	    3079 allocs/op
BenchmarkGraphQuery-8             	    5119	    236804 ns/op	  169890 B/op	    3079 allocs/op
BenchmarkGraphQuery-8             	    5061	    236515 ns/op	  169891 B/op	    3079 allocs/op
BenchmarkGraphQueryScaled-8       	       7	 155655369 ns/op	64245905 B/op	 1010543 allocs/op
BenchmarkGraphQueryScaled-8       	       7	 144522036 ns/op	64258184 B/op	 1010544 allocs/op
BenchmarkGraphQueryScaled-8       	       7	 144733393 ns/op	64263806 B/op	 1010542 allocs/op
BenchmarkGraphQueryScaled-8       	       7	 144718673 ns/op	64260692 B/op	 1010544 allocs/op
BenchmarkGraphQueryScaled-8       	       7	 151022768 ns/op	64266362 B/op	 1010543 allocs/op
BenchmarkDirectDependOnScaled-8   	    3164	    373933 ns/op	  100672 B/op	      11 allocs/op
BenchmarkDirectDependOnScaled-8   	    3237	    374348 ns/op	  100672 B/op	      11 allocs/op
BenchmarkDirectDependOnScaled-8   	    3304	    371669 ns/op	  100672 B/op	      11 allocs/op
BenchmarkDirectDependOnScaled-8   	    3206	    371642 ns/op	  100672 B/op	      11 allocs/op
BenchmarkDirectDependOnScaled-8   	    3178	    372468 ns/op	  100672 B/op	      11 allocs/op
```

(The scaled benchmarks log `scaled fixture symbol count: 12001` during setup;
`BenchmarkChurnContextKernRepo` and other unrelated benchmarks in the same
package also run under `-bench .` but are out of scope for this suite.)

## 6. The 2026-10-19 regression pass and caller-index fix

A 2026-10-19 verification pass measured `BenchmarkGraphQueryScaled` at
**~2.89 s / 1.74 GB / 2.4M allocs per op** — ~900× the documented 3.14 ms —
and `kern impact ArgString` at 4.5 s wall on the live repo. Root cause: the
blast-radius walk (`internal/intel` `blastRadiusWalk`) rebuilt a sourced-caller
map from **all** of `ix.Calls` on every invocation, and `Index.CallersFor`
rescaned **all** of `ix.Callers` (plus `foreignImportBases` / `filePkgIndex`)
once per visited node. Fix (2026-10-19, in this revision):

- `internal/index` gained a lazily-built reverse-edge index (`callerIndex`,
  `sync.Once`-guarded on `indexCache`): `Callers` grouped by simple key,
  sourced-caller buckets by simple target name, plus cached
  `foreignImportBases` / `filePkgIndex`. `callersForLegacy` and the
  `callerImportsDef*` family read the cached tables instead of rescanning.
- `Index.CallersFor` / `Index.CallerDeclarations` iterate the binding
  catalog's `byEndpoint` positions directly instead of rebuilding a sorted
  positions set + `[]exportCall` copy per query (single-root lookups).
- `blastRadiusWalk` consumes the cached sourced-caller buckets instead of
  rebuilding them per invocation.

After the fix the same machine measures the scaled transitive query at
**~145 ms** (20× faster than the regression), the live-repo
`BlastRadius(ArgString)` at **~32 ms steady** (25× faster than the 784 ms
measured before the fix), and `kern impact ArgString` at ~4.6 s wall
(dominated by the documented cold index load + graph build in `app.New`,
not by the blast-radius walk). Determinism is preserved: every result path
still sorts its output (`dedupeSorted` / `sort.Strings`), and map iteration
order never leaks into results.

## 7. Regression gate (`kern bench --check` / `make bench-gate`)

The repo-level companion harness `kern bench` (see
[multi-repo-latency.md](multi-repo-latency.md) for its measured cross-repo
output) mirrors this page's cold-vs-warm methodology against the live repo:
cold index build, warm persisted-store load, and five fixed queries
(symbol search, ranked search, one-hop callers, transitive blast radius,
hub ranking). `make bench-gate` runs it with `--check`, verifying each run
against the versioned thresholds in `cmd/kern/bench_thresholds.json`
(embedded in the binary; `KERN_BENCH_THRESHOLDS` overrides the file path for
testing):

| Check | Level | Guards |
|---|---|---|
| `warm_load_ms` ceiling | fail | the persisted-store fast path every command pays |
| per-query ceilings | fail | the five query latencies |
| `cold_vs_warm_speedup` floor | fail | a warm Load that falls through to a full Build collapses the ratio to ~1x |
| zero-result query | fail | wiring regression (a query returning nothing is not a latency finding) |
| past warn-factor of a ceiling | warn | approaching a ceiling — reported, never fatal |

Ceilings are deliberately generous multipliers over the measured baseline
(warm 174 ms, worst query 91 ms, speedup 4.6x on 2026-10-19), so the gate
fails on regressions, never on machine noise — a machine several times
slower than the baseline still passes. Adjusting a threshold is a
deliberate act: update `cmd/kern/bench_thresholds.json`, and
`TestBenchThresholdsEmbeddedFileParses` fails on a hand-edit that silently
disables a check.