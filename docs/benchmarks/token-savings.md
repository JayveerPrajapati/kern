# kern Token Savings Report — compact context vs naive full-file context

**Status:** [measured] — machine-generated · **Suite:** TS-001 · **Generation date:** 2026-10-17
**Git HEAD:** 94471ebc40ac023136f27d1ce87f3c17fb802ebd
**Tokenizer:** `internal/tokenize.Count` (deterministic offline BPE, cl100k_base)
**Numeric checksum (sha256):** 63d56f32f648bec1f4025f352fe99d47d17606642284ffd58538a5a432751d06
**Reproduce:** `go test ./internal/tokstats -run TestTokenSavingsReport -update`

## 1. Methodology

- **Fixture:** a deterministic in-test tree — `lib` (hub package: store, cache, config, logger), `app` (HTTP server + user handlers + wiring), `main` (driver). Written to `t.TempDir()` and indexed with `index.Build` on every run; sources live in `internal/tokstats/savings_report_test.go` (`savingsFixtureFiles`).
- **Queries:** 5 hub symbols × 3 modes = 15 samples. Hub symbols: `NewStore`, `Store.Save`, `Store.Get`, `LoadConfig`, `Cache.Put`.
- **Modes:** `graph` (`Index.Graph`), `context` (`Index.Context(sym, 12)`), `export-json` (`GraphResult.GraphJSON` via `Index.Neighborhood` — the `--json`/`--graphml` machine export, not an agent-facing render).
- **Naive baseline:** the concatenated FULL source of every file the query touches (definition, caller and callee files — what a naive agent would paste). File set and order mirror `TokenSavingsForNeighborhood`'s walk, so the export-json baseline equals that helper exactly; graph/context use the same all-touched-files baseline (stricter than their def-file-only helper baseline).
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
| NewStore | export-json | 2499 | 1372 | 45% vs 4 files read raw |
| Store.Save | graph | 2153 | 73 | 96% vs 3 files read raw |
| Store.Save | context | 2153 | 289 | 86% vs 3 files read raw |
| Store.Save | export-json | 2153 | 1767 | 17% vs 3 files read raw |
| Store.Get | graph | 2153 | 67 | 96% vs 3 files read raw |
| Store.Get | context | 2153 | 299 | 86% vs 3 files read raw |
| Store.Get | export-json | 2153 | 1562 | 27% vs 3 files read raw |
| LoadConfig | graph | 1090 | 129 | 88% vs 2 files read raw |
| LoadConfig | context | 1090 | 400 | 63% vs 2 files read raw |
| LoadConfig | export-json | 1090 | 3073 | -181% vs 2 files read raw |
| Cache.Put | graph | 1476 | 69 | 95% vs 2 files read raw |
| Cache.Put | context | 1476 | 312 | 78% vs 2 files read raw |
| Cache.Put | export-json | 1476 | 1584 | -7% vs 2 files read raw |

## 4. Aggregates

| mode | naive tokens | compact tokens | savings % (vs naive tokens) |
|---|---|---|---|
| graph | 9371 | 397 | 95% |
| context | 9371 | 1593 | 83% |
| export-json | 9371 | 9358 | 0% |
| **overall** | **28113** | **11348** | **59%** |

## 5. Verdict

For this fixture class — a small multi-package Go service (lib hub + app + main consumers), 15 samples — the claim of **substantial savings** is **BACKED**.

- Overall savings (all three modes incl. export-json): **59% vs all touched files** (28113 → 11348 tokens).
- Per-mode (vs naive full-file paste): graph 95%, context 83% — the agent-facing compact renders; weakest context at 83%. export-json 0%: the `--json`/`--graphml` machine serialization, measured for transparency — verbose by design, never pasted into agent context.
- **Surgical context**: compact output stays at or below 20% of the naive full-file paste (≥80% savings) in **8 of 15** samples.
- kern's graph/context outputs replace multi-file full-source pastes with a fraction of the tokens while retaining the same symbol, caller and callee information. On this fixture the savings claim is quantitative, not marketing.
