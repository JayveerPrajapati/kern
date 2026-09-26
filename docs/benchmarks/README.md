# kern Benchmark Suite Index

**Suite:** benchmarks · **Status:** multiple baselines, see each suite's page

This directory holds kern's benchmark suites. Each suite answers a different
marketing claim with a machine-checkable protocol, honest numbers, and a
documented methodology — deterministic, stdlib-only, zero network, no LLM
calls in the measurements.

## Suites

| Suite | Page | Claim under test |
|---|---|---|
| Semantic retention | [`semantic-retention.md`](semantic-retention.md) | Compression keeps *meaning*, not just tokens (tier1 retention, symbol recall, call-graph fidelity, loss-adjusted savings) |
| Graph latency | [`graph-latency.md`](graph-latency.md) | "Sub-millisecond graph": cold/warm index + query latency, honest verdict at fixture and ~12k-symbol scale |
| Cold-start scale | [`cold-start.md`](cold-start.md) | "Slower than grep": one-shot cold-build/cold-load vs `grep -rn` across repo sizes (83 → 7,819 files), same-tree protocol |
| Telemetry audit | [`telemetry-audit.md`](telemetry-audit.md) | Zero network egress in core packages (CI-enforced static import scan) |
| Token savings | [`token-savings.md`](token-savings.md) | Compact context vs naive full-file context token reduction (machine-generated report) |
| Duplication detector | [`../duplication-benchmark.md`](../duplication-benchmark.md) | AST duplication scanner confusion matrix and precision floors |

## The four benchmark dimensions (semantic retention)

| # | Dimension | Question | Headline gate |
|---|---|---|---|
| 1 | **Context window efficiency** | Does compression retain what matters? | tier1 marker retention ≥ 0.95 per fixture |
| 2 | **Symbol recall** | Can an agent still find symbols after budget fitting? | recall@50% budget ≥ 0.60 per code fixture |
| 3 | **Call graph fidelity** | Are who-calls-whom relationships preserved? | suite-mean endpoint fidelity ≥ 0.55, fabricated edges = 0 |
| 4 | **Token savings** | Is the reduction real, or information destruction? | loss-adjusted savings ≥ 0.40 (logs) / ≥ 0.10 (prompts) |

## Quick facts

- **Deterministic.** No LLM calls, no network, no clocks in the measurements.
- **Zero external dependencies.** Everything is Go stdlib + plain JSON/text.
- **Live harness:** `kern bench` measures this repo's cold/warm index load and
  query latencies and writes `.kern/bench.json` (rendered by the web console's
  /benchmarks page); `make bench-latency` wraps it.
- **Compression suite:** token-reduction benchmarks still run with
  `go run ./evaluate/bench` (or `make bench`), wired into CI.

## Directory map

```
docs/benchmarks/
  README.md              this suite index
  semantic-retention.md  detailed spec (metrics, gates, baseline) + fixtures map
  graph-latency.md       cold/warm + query latency methodology and results
  telemetry-audit.md     zero-network-egress audit (CI-gated)
  token-savings.md       machine-generated token-savings report
  fixtures/              semantic-retention corpus (code/logs/prompts + ground truth)
```

## Related

- [`cmd/kern/cmd_bench.go`](../../cmd/kern/cmd_bench.go) — the `kern bench` latency harness.
- [`docs/benchmarks/graph-latency.md`](graph-latency.md) — methodology the harness mirrors.
- [`internal/bpreceipt/metrics/g12_test.go`](../../internal/bpreceipt/metrics/g12_test.go) — G12 cold-vs-warm latency tests.
- [`evaluate/bench/main.go`](../../evaluate/bench/main.go) — token-reduction benchmarks (CI-gated).