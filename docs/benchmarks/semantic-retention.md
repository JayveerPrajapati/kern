# Semantic Retention Benchmarks

**Status:** Baseline established (2026-09-09) · **Suite:** P1-001
**Owner:** kern maintainers · **Tokenizer:** `internal/tokenize.Count` (deterministic, offline, stdlib-only)

This document is the detailed specification for kern's **semantic retention**
benchmark suite. It defines what we measure, how we measure it, and what
"good" means — for every one of the four benchmark dimensions. It is the
companion to [`README.md`](README.md) (overview) and
[`fixtures/`](fixtures/README.md) (corpus and ground truth).

## 1. Purpose

kern's existing benchmarks (`evaluate/bench`, reported in the README) prove
**how much** kern compresses: tokens in vs tokens out, against fixed inline
corpora, with hard gates wired into CI. They do not prove **that meaning
survives** the compression.

This suite measures **semantic retention**: the fraction of *important
information* that survives context optimization. A compressor that deletes
everything achieves 100% token savings and 0% retention — the loss-adjusted
score below is 0. The suite exists to catch exactly that failure mode.

It is **complementary, not a replacement**:

| Existing suite | Measures | This suite | Measures |
|---|---|---|---|
| `evaluate/bench` | token reduction, gate pass/fail | semantic retention | information survival |
| `evaluate/calibration` | review-risk threshold calibration | semantic retention | symbol/edge/marker survival |
| `internal/*_test.go` | unit behavior of one function | semantic retention | end-to-end retention of real-shaped corpora |

## 2. Scope

**In scope**

- Four dimensions: context window efficiency, symbol recall, call graph
  fidelity, token savings (loss-adjusted).
- Logs, prompts, and Go source corpora (see `fixtures/`).
- Deterministic, rule-based operators: `internal/compress`, `internal/budget`,
  `internal/terse`. No LLM calls, no network, no external dependencies
  (Go stdlib only).

**Out of scope**

- LLM-judged "quality" scores (non-deterministic by design).
- Benchmarks of semantic *cache* hit rates (`internal/semcache`) — that is
  cache behavior, not retention; `semcache.Similarity` may be used later to
  score *fuzzy* retention, but the core suite is exact substring retention.
- Byte-level compression benchmarks (covered by `evaluate/bench`).

## 3. Terminology

| Term | Definition |
|---|---|
| **Source** | The fixture as written (ground truth input). |
| **Operator** | The kern function under test (e.g. `compress.CompressLog`). |
| **Output** | The operator's result for a given source. |
| **Token count** | `internal/tokenize.Count` — deterministic offline BPE. |
| **Savings** | `1 − tokens(output) / tokens(source)`. |
| **Marker** | A ground-truth substring that must survive (see tiering below). |
| **Retention** | `|markers preserved in output| / |markers in ground truth|`. |
| **Fabrication** | Content in the output that is not derivable from the source (must be 0). |

Marker tiers (`fixtures/expected/retention.json`):

- **tier1 — must survive:** errors, failure messages, stack-frame payloads,
  unique technical identifiers, paths, config values.
- **tier2 — should survive:** warnings, unique operational events.
- **tier3 — watch:** structural markers (e.g. `goroutine N` headers) whose
  loss is acceptable; kept only for regression visibility.

## 4. The four dimensions

### 4.1 Context Window Efficiency

**Question:** how well does kern compress a context while retaining the
information that matters?

- Operator: `compress.CompressLog(source, compress.Options{})` for logs;
  `compress.CompressPrompt(source)` for prompts (`internal/compress`).
- Metrics:
  - `savings = 1 − tokens(out) / tokens(in)`
  - `retention_t1 = tier1 markers preserved / tier1 markers expected`
  - `efficiency = savings × retention_t1` (both must be high)
- **Hard gates:** `retention_t1 ≥ 0.95` per log/prompt fixture;
  `savings ≥ 0.15` per log fixture, `≥ 0.03` per prompt fixture;
  suite-mean `efficiency ≥ 0.40` for logs, `≥ 0.10` for prompts.
- Baseline (2026-09-09) in §7.

### 4.2 Symbol Recall

**Question:** after budget fitting, can an agent still *find* the symbols it
needs?

- Operator: `budget.FitCode(source, budget)` (`internal/budget`) with
  `budget = b × tokens(source)`, `b ∈ {0.25, 0.50, 0.75}`.
- Symbol set: unique names of top-level types, top-level functions, methods,
  and interface methods (ground truth in `fixtures/expected/symbols-*.json`).
- Metrics (at each budget `b`):
  - `recall@b = |expected symbols present in output| / |expected symbols|`
  - `precision = |expected symbols present| / |all identifier-looking tokens present|`
    (a guard against fabrication; must stay ≥ 0.95).
- **Hard gate:** `recall@0.50 ≥ 0.60` per code fixture.
- **Stretch target:** `recall@0.50 ≥ 0.80` (see known gap G2, §8).
- Baseline curve in §7.

### 4.3 Call Graph Fidelity

**Question:** does the compressed context preserve who-calls-whom?

- Operator: same `budget.FitCode(source, 0.50 × tokens(source))` pass as 4.2
  (endpoint mode). A stricter call-site mode (the actual call expression
  survives) is defined for future lossless/terse operators.
- Edge set: direct call sites between fixture symbols, interface-method
  calls qualified with the interface type (ground truth in
  `fixtures/expected/edges-*.json`).
- Metrics:
  - `endpoint_fidelity = |edges with both endpoint names in output| / |edges|`
  - `fabricated_edges = |edges inferable from output but absent from ground truth|`
- **Hard gates:** suite-mean `endpoint_fidelity ≥ 0.55` at `b = 0.50`;
  `fabricated_edges = 0` (rule-based compressors cannot invent calls; this is
  a guard for any future LLM-assisted compressor).
- **Stretch target:** suite-mean `endpoint_fidelity ≥ 0.75`.
- Baseline in §7.

### 4.4 Token Savings (loss-adjusted)

**Question:** is the token reduction real savings, or is it information
destruction in disguise?

- Metrics:
  - `savings` as in 4.1.
  - `loss_adjusted_savings (LAS) = savings × retention_t1`.
    Deleting everything ⇒ LAS = 0. Keeping everything ⇒ LAS = 0.
- **Hard gates:** suite-mean `LAS ≥ 0.40` for logs, `≥ 0.10` for prompts.
- Baseline in §7.

## 5. Measurement protocol

1. **Determinism.** Every metric is a pure function of (fixture, operator,
   budget, tokenizer). No randomness, no clock, no network. Two runs on the
   same commit produce identical numbers.
2. **Tokenizer.** All token counts use `internal/tokenize.Count` (offline
   BPE). Record the commit the numbers were measured on.
3. **Corpus versioning.** Fixtures are immutable once merged. Ground truth
   lives in `fixtures/expected/`; a change to a fixture *requires* updating
   its ground truth in the same change.
4. **Harness.** The suite runs as a Go stdlib-only harness (a future
   `evaluate/retention` package) that:
   - reads `fixtures/manifest.json` to enumerate fixtures and their expected
     ground truth;
   - applies the operator named in the manifest to each fixture;
   - computes the four dimensions' metrics;
   - compares against the gates and prints a PASS/WARN/FAIL report.
   Until the harness exists, the spec + fixtures + this document's baseline
   table are the protocol, and §7 numbers are reproducible with a throwaway
   `go run` program (exact procedure in §9).

## 6. Success criteria (summary)

| Dimension | Metric | Gate (must pass) | Stretch |
|---|---|---|---|
| Context window efficiency | tier1 retention per log/prompt fixture | ≥ 0.95 | — |
| Context window efficiency | savings per log / prompt fixture | ≥ 0.15 / ≥ 0.03 | — |
| Context window efficiency | suite-mean efficiency (logs / prompts) | ≥ 0.40 / ≥ 0.10 | — |
| Symbol recall | `recall@0.50` per code fixture | ≥ 0.60 | ≥ 0.80 |
| Symbol recall | precision (anti-fabrication) | ≥ 0.95 | 1.00 |
| Call graph fidelity | suite-mean endpoint fidelity at `b=0.50` | ≥ 0.55 | ≥ 0.75 |
| Call graph fidelity | fabricated edges | = 0 | = 0 |
| Token savings | suite-mean LAS (logs / prompts) | ≥ 0.40 / ≥ 0.10 | — |

## 7. Baseline (measured 2026-09-09)

Tokenizer: `internal/tokenize.Count`. Operators: `compress.CompressLog` /
`compress.CompressPrompt` (default options), `budget.FitCode`.

### 7.1 Context window efficiency

| Fixture | tokens in | tokens out | savings | tier1 retained | tier2 retained | tier3 retained | efficiency |
|---|---|---|---|---|---|---|---|
| `build-failure.log.txt` | 119 | 98 | 17.6% | 5/5 (100%) | — | 0/1 | 0.176 |
| `chatty-server.log.txt` | 180 | 33 | 81.7% | 2/2 (100%) | 0/1 | 0/1 | 0.817 |
| `verbose-prompt.md.txt` | 185 | 134 | 27.6% | 3/3 (100%) | 1/1 | — | 0.276 |
| `code-review-request.md.txt` | 129 | 122 | 5.4% | 4/4 (100%) | — | — | 0.054 |

Logs: suite-mean savings 49.7% (gate ≥ 0.45 via efficiency), suite-mean
efficiency 0.497 (gate ≥ 0.40) — **PASS**.
Prompts: suite-mean efficiency 0.165 (gate ≥ 0.10) — **PASS**.

### 7.2 Symbol recall (FitCode)

| Fixture | tokens in | recall@25% | recall@50% | recall@75% |
|---|---|---|---|---|
| `ordersvc.go.txt` | 475 | 7/11 (64%) | 9/11 (82%) | 9/11 (82%) |
| `authsvc.go.txt` | 437 | 5/11 (45%) | 7/11 (64%) | 7/11 (64%) |

Gate `recall@0.50 ≥ 0.60`: ordersvc 82% **PASS**, authsvc 64% **PASS**
(thin). Precision = 1.00 on both (no fabricated symbols observed).

### 7.3 Call graph fidelity (endpoint mode, `b = 0.50`)

| Fixture | edges | endpoints retained | endpoint fidelity |
|---|---|---|---|
| `ordersvc.go.txt` | 4 | 3 | 0.75 |
| `authsvc.go.txt` | 6 | 3 | 0.50 |

Suite-mean 0.625 (gate ≥ 0.55) — **PASS**. Fabricated edges: 0 — **PASS**.

### 7.4 Token savings (loss-adjusted)

| Fixture | savings | tier1 retention | LAS |
|---|---|---|---|
| `build-failure.log.txt` | 0.176 | 1.00 | 0.176 |
| `chatty-server.log.txt` | 0.817 | 1.00 | 0.817 |
| `verbose-prompt.md.txt` | 0.276 | 1.00 | 0.276 |
| `code-review-request.md.txt` | 0.054 | 1.00 | 0.054 |

Logs suite-mean LAS 0.497 (gate ≥ 0.40) — **PASS**.
Prompts suite-mean LAS 0.165 (gate ≥ 0.10) — **PASS**.

## 8. Known gaps (documented findings → backlog)

These are real behaviors surfaced by the baseline, not spec errors. They are
the actionable output of the suite:

- **G1 — WARN-level lines can be dropped by `CompressLog`.** In
  `chatty-server.log.txt` the unique WARN line `slow query took 512ms` (tier2)
  is lost at default options; tier1 errors survive. Slow-query warnings are
  operationally valuable. Candidate follow-up: ensure unique warn/error lines
  are clustered but never folded into chatter.
- **G2 — paths/config on INFO lines are dropped.** `config.yml` (tier3) in
  `chatty-server.log.txt` is lost with the INFO chatter it sat on. Tier3 is
  watch-only today, but `compress.CompressPrompt` retains paths (see
  `verbose-prompt` 3/3) — aligning log behavior with prompt behavior would
  close this.
- **G3 — `budget.FitCode` symbol recall is budget-constrained.** `recall@0.50`
  is 82%/64% and *plateaus* at 75% budget (FitCode emits a fixed-size core,
  extra budget is unused). `authsvc` misses `NewAuthService`, `HashPassword`,
  `VerifyPassword`, `IssueToken` — helper functions whose signatures are not
  in the "core + important" retention set. Candidate follow-up: include
  exported-function signatures reachable from retained entry points.
- **G4 — short inputs compress little.** `code-review-request.md.txt`
  (129 tokens) yields 5.4% savings and `build-failure.log.txt` 17.6% —
  expected for small texts; gates are floored accordingly, and short-input
  behavior is guarded separately by unit tests.

## 9. Reproducing the baseline

```sh
# current head, 2026-09-09
go run ./evaluate/bench                     # existing compression suite (unchanged)
# baseline procedure used for this document:
#   read each fixture with os.ReadFile,
#   count tokens with internal/tokenize.Count,
#   apply compress.CompressLog / CompressPrompt / budget.FitCode at b* tokens,
#   substring-match markers/symbols/edges from fixtures/expected/.
```

A committed harness (`evaluate/retention`) is the follow-up task; until then
the procedure above reproduces every number in §7 deterministically.

## 10. Anti-goals (what "PASS" must not mean)

- **No zero-information wins.** LAS ties savings to retention: 100% savings
  with 0% retention fails the suite.
- **No overfitting to fixtures.** Fixtures are synthetic but shaped like real
  logs/prompts/code; gates are calibrated to the 2026-09-09 baseline with
  headroom, so a regression — not a fixture edit — is what moves a gate.
- **No nondeterminism.** A gate that flips between runs is a bug in the
  harness, not a pass/fail signal.

## 11. Related documents

- [`README.md`](README.md) — suite overview and directory map.
- [`fixtures/README.md`](fixtures/README.md) — corpus layout, ground-truth
  derivation, how to add fixtures.
- `evaluate/bench/main.go` — the existing (compression-focused) benchmark.
- `internal/compress`, `internal/budget`, `internal/terse`, `internal/tokenize`
  — operators under test.