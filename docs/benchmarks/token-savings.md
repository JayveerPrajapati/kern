# kern Token Savings Report — compact context vs naive full-file context

**Status:** machine-generated · **Suite:** TS-001 · **Generation date:** 2026-09-23
**Git HEAD:** 3866e5a92d167a32be538e33f115bdc15253016c
**Tokenizer:** `internal/tokenize.Count` (deterministic offline BPE, cl100k_base)
**Numeric checksum (sha256):** 2566ac3251d3150342c6051f6fab2d99918dd23fb339eef5c72047aeac1ca247
**Reproduce:** `go test ./internal/index -run TestTokenSavingsReport -update`

## 1. Methodology

- **Fixture:** a deterministic in-test tree — `lib` (hub package: store, cache, config, logger), `app` (HTTP server + user handlers + wiring), `main` (driver). Written to `t.TempDir()` and indexed with `index.Build` on every run; sources live in `internal/index/savings_report_test.go` (`savingsFixtureFiles`).
- **Queries:** 5 hub symbols × 3 modes = 15 samples. Hub symbols: `NewStore`, `Store.Save`, `Store.Get`, `LoadConfig`, `Cache.Put`.
- **Modes:** `graph` (`Index.Graph`), `context` (`Index.Context(sym, 12)`), `neighborhood` (`GraphResult.GraphJSON` via `Index.Neighborhood`).
- **Naive baseline:** the concatenated FULL source of every file the query touches (definition, caller and callee files — what a naive agent would paste). File set and order mirror `TokenSavingsForNeighborhood`'s walk, so the neighborhood baseline equals that helper exactly; graph/context use the same all-touched-files baseline (stricter than their def-file-only helper baseline).
- **Counter:** `internal/tokenize.Count` — the same deterministic counter kern's `TokenSavingsFor*` helpers use.
- **Savings:** `floor((naive − compact) / naive × 100)`, computed by `computeTokenSavings` (the engine behind all three helpers); the compact output counted includes kern's appended stats summary line where the API emits one.
- **Reproduction (one command):**

```
go test ./internal/index -run TestTokenSavingsReport -update   # write the report
go test ./internal/index -run TestTokenSavingsReport -count=1  # validate against the golden checksum
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

| symbol | mode | naive tokens | compact tokens | savings % |
|---|---|---|---|---|
| NewStore | graph | 2499 | 53 | 97% |
| NewStore | context | 2499 | 273 | 89% |
| NewStore | neighborhood | 2499 | 537 | 78% |
| Store.Save | graph | 3705 | 89 | 97% |
| Store.Save | context | 3705 | 300 | 91% |
| Store.Save | neighborhood | 3705 | 1039 | 71% |
| Store.Get | graph | 3705 | 83 | 97% |
| Store.Get | context | 3705 | 310 | 91% |
| Store.Get | neighborhood | 3705 | 975 | 73% |
| LoadConfig | graph | 1090 | 123 | 88% |
| LoadConfig | context | 1090 | 387 | 64% |
| LoadConfig | neighborhood | 1090 | 1107 | -1% |
| Cache.Put | graph | 2476 | 96 | 96% |
| Cache.Put | context | 2476 | 312 | 87% |
| Cache.Put | neighborhood | 2476 | 1107 | 55% |

## 4. Aggregates

| mode | naive tokens | compact tokens | savings % |
|---|---|---|---|
| graph | 13475 | 444 | 96% |
| context | 13475 | 1582 | 88% |
| neighborhood | 13475 | 4765 | 64% |
| **overall** | **40425** | **6791** | **83%** |

## 5. Verdict

For this fixture class — a small multi-package Go service (lib hub + app + main consumers), 15 samples — the claim of **substantial savings** is **BACKED**.

- Overall savings: **83%** (40425 → 6791 tokens).
- Per-mode: graph 96%, context 88%, neighborhood 64% (weakest mode: neighborhood at 64%).
- **Surgical context**: compact output stays at or below 20% of the naive full-file paste (≥80% savings) in **9 of 15** samples.
- kern's graph/context/neighborhood outputs replace multi-file full-source pastes with a fraction of the tokens while retaining the same symbol, caller and callee information. On this fixture the savings claim is quantitative, not marketing.
