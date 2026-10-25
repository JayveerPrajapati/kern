# File & function LOC gate — the per-file ledger

`go test ./internal/architecture/` (TestFileAndFunctionLOCGates) parses this
table and fails on divergence — the per-file counterpart of the package
ledger in ARCHITECTURE.md, with the same ratchet rule:

- every listed file's line count (newlines+1, same counting rule as the
  package LOC baseline) must stay at or below its cap;
- every function/method in every non-vendored, non-test `.go` file in the
  tree must stay at or below the global function cap.

Crossing a cap forces decomposition — never a cap raise (the same rule the
package ledger enforces, and for the same reason: a 3.4k-line engine.go fits
inside a 12,900-line package cap today and would otherwise grow forever).

Caps are set **above** the measured floor (baseline × ~1.2, rounded up) so
this gate binds *future* growth rather than today's code — exactly the
baseline×budget rule the package ledger documents. The LOC baseline column
is informational and refreshed when a measured value drifts; the caps are
the contract. The `function` row carries the single global function/method
cap — exactly one such row must exist.

| scope | LOC baseline | cap |
|---|---|---|
| `function` (any Go function/method, all packages) | 635 | 800 |
| `internal/index/engine.go` | 3431 | 4100 |
| `internal/mcp/server.go` | 1908 | 2300 |
| `internal/verification/engine.go` | 1408 | 1700 |
| `internal/doctor/doctor.go` | 1143 | 1400 |
| `internal/verifycmd/verify_run.go` | 1106 | 1400 |
| `internal/lspbridge/bridge.go` | 1004 | 1200 |
| `internal/mcp/highlevel/highlevel.go` | 965 | 1200 |
| `internal/graphcli/cmd_index.go` | 965 | 1200 |
| `cmd/kern/flags.go` | 898 | 1100 |
| `internal/loop/loop.go` | 827 | 1000 |
| `internal/diffgate/diffgate.go` | 800 | 1000 |
| `cmd/kern/cmd_optimize.go` | 769 | 950 |
| `internal/execution/cli.go` | 769 | 950 |
