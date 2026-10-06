# kern Token Savings Report — compact context vs naive full-file context

**Status:** machine-generated · **Suite:** TS-001 · **Generation date:** 2026-10-06
**Git HEAD:** 4ca344bbe2e978b60c93174aa486dfdcb930b216
**Tokenizer:** `internal/tokenize.Count` (deterministic offline BPE, cl100k_base)
**Numeric checksum (sha256):** a060c3f05643f41140f20a259109012e6a1cdec0ffa9c7b5d12f178acfbb70d1
**Reproduce:** `go test ./internal/tokstats -run TestTokenSavingsReport -update`

## 1. Methodology

- **Fixture:** a deterministic in-test tree — `lib` (hub package: store, cache, config, logger), `app` (HTTP server + user handlers + wiring), `main` (driver). Written to `t.TempDir()` and indexed with `index.Build` on every run; sources live in `internal/tokstats/savings_report_test.go` (`savingsFixtureFiles`).
- **Queries:** 5 hub symbols × 3 modes = 15 samples. Hub symbols: `NewStore`, `Store.Save`, `Store.Get`, `LoadConfig`, `Cache.Put`.
- **Modes:** `graph` (`Index.Graph`), `context` (`Index.Context(sym, 12)`), `neighborhood` (`GraphResult.GraphJSON` via `Index.Neighborhood`).
- **Naive baseline:** the concatenated FULL source of every file the query touches (definition, caller and callee files — what a naive agent would paste). File set and order mirror `TokenSavingsForNeighborhood`'s walk, so the neighborhood baseline equals that helper exactly; graph/context use the same all-touched-files baseline (stricter than their def-file-only helper baseline).
- **Labeled denominator:** every savings percentage in this report is annotated with what it is relative to ("N files read raw") so the baseline is never implicit — the same rule every kern savings renderer follows (finding V1).
- **Counter:** `internal/tokenize.Count` — the same deterministic counter kern's `TokenSavingsFor*` helpers use.
- **Savings:** `floor((naive − compact) / naive × 100)`, computed by `computeTokenSavings` (the engine behind all three helpers); the compact output counted includes kern's appended stats summary line where the API emits one.
- **Reproduction (one command):**

```
go test ./internal/tokstats -run TestTokenSavingsReport -update   # write the report
go test ./internal/tokstats -run TestTokenSavingsReport -count=1  # validate against the golden checksum
```

## 2. Fixture

| file | package | approx lines | role |
|---|---|---|---|
| `app/handlers.go` | `app` | 69 | UserHandler calling Store/Cache via field chain |
| `app/server.go` | `app` | 75 | HTTP Server calling Store methods via field chain |
| `app/wiring.go` | `app` | 64 | composition root: BuildDeps/DefaultDeps/RunServer |
| `lib/cache.go` | `lib` | 106 | Cache + eviction, called by Store and app |
| `lib/config.go` | `lib` | 94 | Config, DefaultConfig, LoadConfig + parser helpers |
| `lib/logger.go` | `lib` | 110 | Logger + ParseLevel, called everywhere |
| `lib/store.go` | `lib` | 134 | hub: Store struct, NewStore, Save/Get/Open/Close + helpers |
| `main/main.go` | `main` | 81 | driver: Runner + main(), direct lib consumer |

## 3. Per-symbol × per-mode results

| symbol | mode | naive tokens | compact tokens | savings % vs baseline |
|---|---|---|---|---|
| NewStore | graph | 2499 | 59 | 97% vs 4 files read raw |
| NewStore | context | 2499 | 293 | 88% vs 4 files read raw |
| NewStore | neighborhood | 2499 | 547 | 78% vs 4 files read raw |
| Store.Save | graph | 3705 | 95 | 97% vs 6 files read raw |
| Store.Save | context | 3705 | 335 | 90% vs 6 files read raw |
| Store.Save | neighborhood | 3705 | 1049 | 71% vs 6 files read raw |
| Store.Get | graph | 3705 | 89 | 97% vs 6 files read raw |
| Store.Get | context | 3705 | 345 | 90% vs 6 files read raw |
| Store.Get | neighborhood | 3705 | 985 | 73% vs 6 files read raw |
| LoadConfig | graph | 1090 | 129 | 88% vs 2 files read raw |
| LoadConfig | context | 1090 | 400 | 63% vs 2 files read raw |
| LoadConfig | neighborhood | 1090 | 1117 | -2% vs 2 files read raw |
| Cache.Put | graph | 2476 | 102 | 95% vs 4 files read raw |
| Cache.Put | context | 2476 | 361 | 85% vs 4 files read raw |
| Cache.Put | neighborhood | 2476 | 1117 | 54% vs 4 files read raw |

## 4. Aggregates

| mode | naive tokens | compact tokens | savings % (vs naive tokens) |
|---|---|---|---|
| graph | 13475 | 474 | 96% |
| context | 13475 | 1734 | 87% |
| neighborhood | 13475 | 4815 | 64% |
| **overall** | **40425** | **7023** | **82%** |

## 5. Verdict

For this fixture class — a small multi-package Go service (lib hub + app + main consumers), 15 samples — the claim of **substantial savings** is **BACKED**.

- Overall savings: **82% vs all touched files** (40425 → 7023 tokens).
- Per-mode (vs naive full-file paste): graph 96%, context 87%, neighborhood 64% (weakest mode: neighborhood at 64%).
- **Surgical context**: compact output stays at or below 20% of the naive full-file paste (≥80% savings) in **9 of 15** samples.
- kern's graph/context/neighborhood outputs replace multi-file full-source pastes with a fraction of the tokens while retaining the same symbol, caller and callee information. On this fixture the savings claim is quantitative, not marketing.
