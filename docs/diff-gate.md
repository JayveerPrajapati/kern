# kern diff-gate — Deterministic Diff Gate (KERN-P2-003)

`kern diff-gate` runs **local, deterministic checks** on the working-tree diff
(staged + unstaged vs HEAD) and returns structured verdicts. It is
**advisory by default**: warnings never fail the exit code. A `--blocking`
mode elevates warnings to a hard failure for protected CI.

## Usage

```
kern diff-gate [flags]

Flags:
  --root=DIR        Repository root (default: ".")
  --timeout=SECS    Max runtime in seconds for the whole validation (default: 120)
  --blocking        Elevate WARN findings to BLOCK (exit 1) for protected CI
  --json            Emit structured JSON verdicts
  --init-baseline   Write the MCP tool-schema baseline and report PASS
  --no-tests        Skip the expensive tests:build-test check (fast advisory runs)
```

## The 8 checks

| Check | Gate | Enforcement | What it verifies |
|---|---|---|---|
| `format:gofmt` | G30 | warn | Changed `.go` files are gofmt-formatted (`gofmt -l`) |
| `vulnerability:sec` | G31 | warn | Changed files trigger no vulnerability patterns (in-house `sec` scanner; error→BLOCK, warning→WARN, info→INFO) |
| `schema:drift` | G32 | warn | MCP tool-schema baseline (`.kern/diff-gate/tool-schemas.json`) matches the live catalog fingerprint (name/phase/risk/InputSchema sha256) |
| `exec:unsafe` | G33 | warn | Changed non-test `.go` files do not import `os/exec`, call `exec.Command`, or contain `sh -c` |
| `changelog:missing` | G34 | warn | Non-doc source changes (not `*.md`, `docs/`, `*.json`) carry a `CHANGELOG.md` entry |
| `catalog:drift` | G35 | **block** | MCP catalog (`mcp.ToolNames()`) matches the opencode plugin tool set (`internal/setup/assets/plugin/kern.ts`) |
| `secret:scan` | G3 (reused) | block* | Reused from `kern check`: secrets in changed files (kern `sec`, in-house fallback) |
| `tests:build-test` | G8 (reused) | block* | Reused from `kern check`: sandboxed `go build ./...` + `go test ./...` in an isolated worktree (skip with `--no-tests`) |

\* Reused checks keep their Blueprint enforcement; the diff-gate *defaults* to
advisory for its own WARN-level checks. `catalog:drift` is a hard block: the
plugin surface silently lagging the universal catalog is a real drift guard.

## Advisory vs blocking

- **Advisory (default):** WARN findings never change the exit code — the gate
  reports them so humans can review, but a working tree with advisory issues
  still exits 0.
- **Blocking (`--blocking`):** a WARN aggregate is elevated to BLOCK (exit 1),
  for protected CI branches where warnings must gate the merge.

## Exit codes

Exit codes follow the Blueprint aggregate semantics:

| Exit | Meaning |
|---|---|
| 0 | PASS / WARN (advisory) / SKIP |
| 1 | BLOCK (a block-severity finding, or `--blocking` elevation) |
| 2 | ERROR (tool failure, usage error, or cannot discover the diff) |

## Schema baseline

`schema:drift` fingerprints the MCP tool catalog deterministically
(`sha256(name, phase, risk, canonical InputSchema JSON)`, sorted by tool name)
and compares it against `<root>/.kern/diff-gate/tool-schemas.json`:

- First run: `kern diff-gate --init-baseline` writes the baseline and reports
  PASS "baseline initialized".
- Subsequent runs: added/removed/changed tools are reported as advisory WARN
  findings. Re-run `--init-baseline` after intentionally changing tool
  contracts (and review unintended drift).

## Notes

- Reuses `kern check`'s secret + tests gates (G3/G8) via the blueprint engine;
  adds formatting, vulnerabilities, schema drift, unsafe execution, changelog,
  MCP catalogue drift.
- The catalog-drift check reimplements the same deterministic comparison as
  the `TestPluginMatchesMCPCatalog` parity test (regex over
  `internal/setup/assets/plugin/kern.ts`) — it never edits the plugin file.
- All checks are stdlib-only and deterministic: no network, no LLM, no
  external services.