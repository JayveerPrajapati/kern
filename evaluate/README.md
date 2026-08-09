# evaluate/ — kern benchmark harness

Reproducible token-reduction and retrieval numbers for every compression
surface. The idea (and the honesty bar) comes from
[code-review-graph](https://github.com/tirth8205/code-review-graph)'s published
36-376x benchmarks, which set the standard for making agent-tool claims
measurable.

## Run

```sh
go run ./evaluate/bench              # report against this repo's docs
go run ./evaluate/bench -root ../some-project
make bench                           # gate tests + report
```

Exit code is `0` only when every hard gate passes; otherwise the failed gates
are listed on stderr.

## What it measures

| Metric | Input | Why it matters |
|---|---|---|
| `optimize prompt` | verbose multi-line user prompt | deterministic fluff stripping before the LLM |
| `optimize log` | timestamped service log with a stack trace | keeps errors/frames, drops chatter |
| `optimize output (terse)` | verbose model reply with code | LLM-output compression (Context Mode parity) |
| `budget fit` | the same log, hard-capped at 40 tokens | head + important-line retention |
| `retrieval recall@5` | queries → docs index | known needle → expected file in top-5 |

The corpora are fixed strings shipped in `bench/main.go` — no network, no
flaky downloads, byte-for-byte reproducible across machines.

## Honesty rules (self-imposed)

1. **Token reduction vs. the input kern is given**, never a LOC→LLM-input
   ratio. When comparing to other tools' marketing numbers, normalize both the
   same way.
2. **Line-structured inputs only.** Kern's deterministic path is line-based;
   a single wall-of-text paragraph is a degenerate case for *every* line-based
   optimizer, so the harness doesn't measure it.
3. **Gates are regression tripwires, not marketing targets.** They are set to
   catch a *drop* from today's real behavior (`internal/compress`,
   `internal/terse`, `internal/optimize`), and are re-checked by
   `TestGatesAreMet` on every `go test ./...`.

## Structure

- `bench/main.go` — harness: deterministic corpora, metrics, recall test, report
- `bench/main_test.go` — gate + degeneracy guards wired into `go test ./...`
