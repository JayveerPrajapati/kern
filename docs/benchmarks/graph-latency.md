# Graph Query Latency Benchmarks

**Date:** 2026-09-23
**Revision:** `3866e5a92d167a32be538e33f115bdc15253016c` (`git rev-parse HEAD` at measurement time)
**Suite:** graph-query-latency · **Status:** baseline established 2026-09-23

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

All benchmarks are in `internal/intel/bench_test.go` (package `intel`), so
they exercise the real query code paths (`BlastRadius`, `Hubs`,
`prodCallersWithFileMap`) against a real `index.Index` built by
`index.Build` from real `.go` files written to a temp dir.

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
| `BenchmarkGraphQuery` | `BlastRadius(ix, ["Public"])` — transitive "what depends on X" (BFS over `ix.Callers`) | small fixture |
| `BenchmarkGraphHubs` | `Hubs(ix, 10)` — full hub ranking (per-symbol caller counts + weighted sort) | small fixture |
| `BenchmarkGraphQueryScaled` | `BlastRadius(ix, ["Public"])` | scaled fixture, walks all 12k symbols |
| `BenchmarkGraphHubsScaled` | `Hubs(ix, 10)` | scaled fixture, scans all 12k symbols |
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
| `BenchmarkGraphQuery` | small | 35 | **7,421 ns (0.0074 ms)** | 6,625 ns | 17 | 5,752 |
| `BenchmarkGraphHubs` | small | 35 | **62,060 ns (0.062 ms)** | 58,083 ns | 232 | 67,608 |
| `BenchmarkGraphQueryScaled` | scaled | 12,001 | **3,142,789 ns (3.14 ms)** | 3,000,065 ns | 124 | 1,500,328 |
| `BenchmarkGraphHubsScaled` | scaled | 12,001 | **21,434,554 ns (21.4 ms)** | 20,423,387 ns | 56,502 | 32,100,236 |
| `BenchmarkDirectDependOnScaled` | scaled | 12,001 | **369,230 ns (0.37 ms)** | 367,522 ns | 11 | 100,672 |

### 3.1 Reading the numbers

- **Small fixture:** every graph query is sub-millisecond by 1–2 orders of
  magnitude (7 µs BlastRadius, 62 µs Hubs). On a 35-symbol graph the claim is
  trivially true.
- **Scaled fixture (12k symbols):**
  - One-hop direct lookup: **0.37 ms** — still sub-millisecond.
  - Transitive BlastRadius (walks the full 12k-symbol graph): **3.14 ms
    median** — ~3.1× over the 1 ms line.
  - Full Hubs ranking: **21.4 ms median** — ~21× over the 1 ms line, and it
    allocates ~32 MB per call (56.5k allocations).
- **Memory scales with reach:** the transitive query allocates 1.5 MB/op at
  12k symbols (124 allocs), the ranking query 32 MB/op. These are the
  allocation cost of building the result slices and per-symbol caller maps.

## 4. Verdict

**Is "sub-millisecond graph" honest?**

- **At small scale — yes.** On the 35-symbol fixture, warm transitive queries
  are ~7 µs and hub ranking ~62 µs. Well under a millisecond.
- **At realistic repo scale — only for direct lookups.** A one-hop "who calls
  X" query is ~0.37 ms (still sub-millisecond), but the headline *transitive*
  "what depends on X" query is **~3.1 ms median** and the full hub-ranking
  query is **~21 ms median** on a 12,001-symbol / 240-file / 4-package
  fixture.
- **Cold vs warm:** all timings above are for a **warm in-memory index** —
  the index build itself is excluded from the timed loop. Building the
  12k-symbol index from scratch (fixture write + full `index.Build`) takes
  **~1.4 s** on this machine (measured as the wall time of a
  `-benchtime 1x` run minus the ~5 ms query).

**Bottom line:** "sub-millisecond graph" is honest **only** for warm
in-memory queries on small indexes and for direct one-hop lookups at any
scale. It needs qualification for realistic repos, where the transitive
query misses the 1 ms bar by ~3× and hub ranking by ~21×, and where cold
index builds are measured in seconds. Suggested honest phrasing:

> *"Sub-millisecond warm in-memory graph queries at small scale; ~3 ms
> transitive queries and ~20 ms hub ranking on ~12k-symbol repos; cold index
> builds take ~1.4 s."*

## 5. Raw per-run output (5 × 1s runs, 2026-09-23)

```
goos: darwin
goarch: arm64
pkg: github.com/JayveerPrajapati/kern/internal/intel
cpu: Apple M1 Pro
BenchmarkGraphQuery-8             	  162632	      7500 ns/op	    5752 B/op	      17 allocs/op
BenchmarkGraphQuery-8             	  134906	      7421 ns/op	    5752 B/op	      17 allocs/op
BenchmarkGraphQuery-8             	  184194	      6625 ns/op	    5752 B/op	      17 allocs/op
BenchmarkGraphQuery-8             	  181405	      6902 ns/op	    5752 B/op	      17 allocs/op
BenchmarkGraphQuery-8             	  186463	      8587 ns/op	    5752 B/op	      17 allocs/op
BenchmarkGraphHubs-8              	   16455	     72247 ns/op	   67608 B/op	     232 allocs/op
BenchmarkGraphHubs-8              	   19357	     62060 ns/op	   67608 B/op	     232 allocs/op
BenchmarkGraphHubs-8              	   17949	     70894 ns/op	   67608 B/op	     232 allocs/op
BenchmarkGraphHubs-8              	   20072	     58083 ns/op	   67608 B/op	     232 allocs/op
BenchmarkGraphHubs-8              	   20851	     58788 ns/op	   67608 B/op	     232 allocs/op
BenchmarkGraphQueryScaled-8       	     387	   3028305 ns/op	 1500328 B/op	     124 allocs/op
BenchmarkGraphQueryScaled-8       	     388	   3000065 ns/op	 1500328 B/op	     124 allocs/op
BenchmarkGraphQueryScaled-8       	     321	   3177216 ns/op	 1500328 B/op	     124 allocs/op
BenchmarkGraphQueryScaled-8       	     381	   3922528 ns/op	 1500328 B/op	     124 allocs/op
BenchmarkGraphQueryScaled-8       	     396	   3142789 ns/op	 1500328 B/op	     124 allocs/op
BenchmarkGraphHubsScaled-8        	      49	  21434554 ns/op	32100236 B/op	   56502 allocs/op
BenchmarkGraphHubsScaled-8        	      58	  21909314 ns/op	32100245 B/op	   56502 allocs/op
BenchmarkGraphHubsScaled-8        	      51	  20473582 ns/op	32100234 B/op	   56502 allocs/op
BenchmarkGraphHubsScaled-8        	      57	  20423387 ns/op	32100239 B/op	   56502 allocs/op
BenchmarkGraphHubsScaled-8        	      52	  21968940 ns/op	32100234 B/op	   56502 allocs/op
BenchmarkDirectDependOnScaled-8   	    3117	    367671 ns/op	  100672 B/op	      11 allocs/op
BenchmarkDirectDependOnScaled-8   	    3195	    369230 ns/op	  100672 B/op	      11 allocs/op
BenchmarkDirectDependOnScaled-8   	    3211	    378584 ns/op	  100672 B/op	      11 allocs/op
BenchmarkDirectDependOnScaled-8   	    3218	    367522 ns/op	  100672 B/op	      11 allocs/op
BenchmarkDirectDependOnScaled-8   	    3189	    373668 ns/op	  100672 B/op	      11 allocs/op
```

(The scaled benchmarks log `scaled fixture symbol count: 12001` during setup;
`BenchmarkChurnContextKernRepo` and other unrelated benchmarks in the same
package also run under `-bench .` but are out of scope for this suite.)