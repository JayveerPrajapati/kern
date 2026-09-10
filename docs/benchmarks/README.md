# kern Semantic Retention Benchmarks

**Suite:** P1-001 · **Status:** baseline established 2026-09-09

This directory holds kern's **semantic retention** benchmark suite: how much
*meaning* survives kern's context optimization, not just how many tokens it
removes.

kern already proves token reduction in `evaluate/bench` (compression-focused,
wired into CI). This suite is the complementary question: when a log, prompt,
or slice of code is compressed, does the important information — errors,
stack frames, paths, symbols, call relationships — survive?

## The four benchmark dimensions

| # | Dimension | Question | Headline gate |
|---|---|---|---|
| 1 | **Context window efficiency** | Does compression retain what matters? | tier1 marker retention ≥ 0.95 per fixture |
| 2 | **Symbol recall** | Can an agent still find symbols after budget fitting? | recall@50% budget ≥ 0.60 per code fixture |
| 3 | **Call graph fidelity** | Are who-calls-whom relationships preserved? | suite-mean endpoint fidelity ≥ 0.55, fabricated edges = 0 |
| 4 | **Token savings** | Is the reduction real, or information destruction? | loss-adjusted savings ≥ 0.40 (logs) / ≥ 0.10 (prompts) |

Full formulas, operators, per-fixture gates, stretch targets, and the
measured baseline live in
[`semantic-retention.md`](semantic-retention.md).

## Quick facts

- **Operators under test:** `compress.CompressLog`, `compress.CompressPrompt`,
  `budget.FitCode` (+ `terse.Compress`, `budget.FitLossless` as future modes).
- **Tokenizer:** `internal/tokenize.Count` — deterministic, offline, stdlib-only.
- **Corpus:** six synthetic fixtures — two Go services, two logs, two prompts —
  with hand-audited ground truth in [`fixtures/expected/`](fixtures/expected/).
- **Zero external dependencies.** Everything is Go stdlib + plain JSON/text.
- **Deterministic.** No LLM calls, no network, no clocks in the measurements.

## Directory map

```
docs/benchmarks/
  README.md              this overview
  semantic-retention.md  detailed specification (metrics, gates, baseline)
  fixtures/
    README.md            corpus layout + ground-truth derivation
    manifest.json        machine-readable index of fixtures and expected values
    code/                Go source corpora (symbol recall + call graph)
    logs/                log corpora (context window + token savings)
    prompts/             prompt corpora (context window + token savings)
    expected/            deterministic ground truth (symbols, edges, retention)
```

## Baseline at a glance (2026-09-09)

- Logs: 81.7% savings on the chatty server log with 100% tier1 retention;
  17.6% on the short build-failure log. Suite-mean loss-adjusted savings 0.50.
- Prompts: 27.6% / 5.4% savings, 100% tier1 retention.
- Symbol recall at 50% budget: ordersvc 82%, authsvc 64% (gate ≥ 60%).
- Call graph endpoint fidelity at 50% budget: 0.75 / 0.50, fabricated edges 0.

Known gaps surfaced by the baseline (WARN-line loss, path loss on INFO
lines, FitCode recall plateau) are documented as follow-ups in
[`semantic-retention.md`](semantic-retention.md#8-known-gaps-documented-findings--backlog).

## Running

No harness is committed yet — the spec + fixtures + baseline table are the
protocol. A committed stdlib-only harness (`evaluate/retention`) is the
follow-up task; the reproduction procedure is in
[`semantic-retention.md`](semantic-retention.md#9-reproducing-the-baseline).
The existing compression suite is unchanged and still runs with
`go run ./evaluate/bench` (or `make bench`).

## Related

- [`evaluate/bench`](../../evaluate/bench/main.go) — token-reduction benchmarks (CI-gated).
- [`internal/compress`](../../internal/compress/compress.go) — log/prompt compression.
- [`internal/budget`](../../internal/budget/budget.go) — token-budget fitting.
- [`internal/tokenize`](../../internal/tokenize/tokenize.go) — deterministic token counting.