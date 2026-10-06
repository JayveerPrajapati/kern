# agentic — kern's agentic benchmark harness

Runs the same coding tasks through a headless agent **with and without
kern's tools**, then scores the results deterministically. The design borrows
the honesty bar from ponytail's agentic benchmark: real agent runs, real
workspaces, deterministic scoring, and a `rescore` mode that recomputes
every metric from kept artifacts with **zero API spend**.

This is a dev script — stdlib only, no product surface. It deliberately
avoids importing anything from `internal/` so it stays out of the
architecture ledger.

## Quickstart

```sh
go run ./evaluate/agentic list                              # show arms + tasks
go run ./evaluate/agentic run                              # full run, default arms
go run ./evaluate/agentic run -task validate-input         # one task
go run ./evaluate/agentic rescore <run-dir>                # recompute, no API spend
```

Results land under `evaluate/agentic/results/<timestamp>/` (gitignored):

```
<run-dir>/<arm>/<task>/ws/    the agent's final workspace (kept)
<run-dir>/<arm>/<task>/art/events.jsonl   raw agent event stream (kept)
<run-dir>/<arm>/<task>/art/metrics.json  per-run metrics
<run-dir>/<arm>/<task>/art/check-output.log
<run-dir>/results.json        run-wide summary
```

## Arms

Arms are pure data (`arms.json`) — any headless agent works. The shipped
pair uses opencode; the with/without knob is `--pure`, which disables
external plugins (kern's opencode plugin included):

| arm | command | meaning |
|---|---|---|
| `baseline` | `opencode run --pure --format json {prompt}` | no external plugins, no kern |
| `kern` | `opencode run --format json {prompt}` | kern's tools available |

A literal `{prompt}` argv element is replaced with the task prompt; if no
placeholder is present, the prompt is appended as the final argument.

## Tasks

A task is a directory under `tasks/`:

- `task.md` — the prompt (leading `# Title` is the display title)
- `fixture/` — workspace template, copied fresh per (arm, task) run
- `check.sh` — the deterministic gate (cwd = workspace, exit 0 = pass); it
  encodes correctness **and** safety **and** reuse expectations
- `task.json` — optional, `{"timeout": "15m"}` per-task override

## Scoring (deterministic only)

- **pass** — `check.sh` exit code (bounded at 60s, process-group killed)
- **over-engineering proxy** — files changed and LOC added vs the pristine
  fixture, split into non-test and test buckets (tests are never bloat);
  modified files contribute their full new line count
- **tokens** — extracted from the raw event stream (see honesty notes)
- **duration** — wall-clock agent time

## Honesty notes

- Token extraction is a heuristic over the raw event stream: it matches
  `{"tokens":{"input","output"}}`, `{"usage":{"input","output"}}`, and flat
  `input_tokens`/`output_tokens` at any nesting depth, takes the max across
  shapes within one event, and reports both a per-event **sum** and a
  single-event **max** — agents differ in whether counters are cumulative
  or per-part. When in doubt, trust the raw `events.jsonl` (always kept).
- `rescore` never executes arm commands — that is the no-API-spend contract
  (pinned by `TestRescoreNeverRunsArms`) — but it does re-run `check.sh`,
  so check improvements propagate to old runs.
- Runs are sequential (no parallelism) so arms are not confounded by rate
  limiting or resource contention.
- Every process launch (arm or check) runs under a hard context deadline
  and is killed with its whole process group on timeout.
