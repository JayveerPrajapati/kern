# Semantic Retention — Benchmark Fixtures

Sample test data for the semantic retention benchmark suite
(`docs/benchmarks/semantic-retention.md`). The corpus is small, synthetic,
versioned, and fully deterministic. It is designed to be consumed by a Go
stdlib-only harness that reads the files as plain text.

## Layout

```
fixtures/
  manifest.json            machine-readable index of every fixture
  README.md                this file
  code/                    Go source corpora (symbol recall + call graph)
    ordersvc.go.txt
    authsvc.go.txt
  logs/                    log corpora (context window + token savings)
    build-failure.log.txt
    chatty-server.log.txt
  prompts/                 prompt corpora (context window + token savings)
    verbose-prompt.md.txt
    code-review-request.md.txt
  expected/                deterministic ground truth
    symbols-ordersvc.json  expected symbol set for ordersvc
    symbols-authsvc.json   expected symbol set for authsvc
    edges-ordersvc.json    expected call edges for ordersvc
    edges-authsvc.json     expected call edges for authsvc
    retention.json         important markers + token counts per log/prompt fixture
```

## Why `.go.txt`?

Go source fixtures use a `.go.txt` extension on purpose: the Go toolchain
ignores non-`.go` files, so `go build ./...` and `go test -short ./...` at
the repository root keep passing. A benchmark harness reads the files with
`os.ReadFile` — the extension is irrelevant to it. If you rename a fixture
to `.go`, it must compile, or the repo build breaks.

## Ground truth

Everything under `expected/` was derived by hand-auditing the fixture
sources on 2026-09-09 (see the `derivation` field in each file):

- **Symbols** — every top-level type, top-level function, method, and
  interface method in the fixture, deduplicated to unique names.
  Unexported helpers are excluded from recall (they are not part of the
  exported API surface a compression should preserve), but may appear in
  edge lists.
- **Edges** — direct call sites between fixture symbols. Stdlib calls are
  excluded; interface-method calls are qualified with the interface type
  (e.g. `OrderRepository.Save`).
- **Retention markers** — substrings that must survive compression, tiered
  by importance (see `tier_definitions` in `retention.json`).
- **Token counts** — measured with kern's own deterministic offline
  tokenizer, `internal/tokenize.Count`, not estimated.

## Adding a fixture

1. Add the corpus file under the matching `code/`, `logs/`, or `prompts/`
   subdirectory (`.go.txt` for Go source).
2. Add the ground truth under `expected/` (symbols/edges for code,
   retention markers for logs/prompts).
3. Register the fixture in `manifest.json` with its `id`, `path`, `kind`,
   and the dimensions it exercises.
4. Re-run the benchmark and record the new baseline in
   `docs/benchmarks/semantic-retention.md`.

All fixtures are synthetic; there is no third-party or copyrighted content.