# Agent-Level Evals: With vs Without kern (historical)

**Suite:** benchmarks · **Status:** [historical] — harness deleted 2026-10-18 (`5c9c917e`), numbers below are dated records of runs that no longer reproduce

> [!IMPORTANT]
> **This page is a historical record, not a live suite.** The phase-1
> deterministic harness lived in `internal/agenteval` and both of its gates
> (`TestDeterministicSuite`, `TestAgentEvalsDocFresh`); the package and both
> gates were **deleted** in `5c9c917e` (the P4 pivot removed the agenteval
> package from the tree). The phase-2 LLM numbers are dated runs from
> 2026-10-08 and cannot be re-run against current code. Nothing on this page
> is CI-able today; the claims below stand as published measurements of the
> code that existed then.

The suite answered one question: do agents actually get more done with kern?
It was designed in two phases.

## Phase 1 — deterministic (deleted with the harness)

The same coding tasks solved by two scripted policies over the same fixture
corpus (`internal/agenteval`, deleted in `5c9c917e`):

- **with kern** — resolves each task's targets through the real index
  (`index.Build` over the corpus — the actual product), reads ONLY the file
  it must edit, and pays for the rendered symbol entries as context.
- **no index** — cannot ask for symbols, so it reads the whole corpus for
  every task: the grep-and-read loop kern exists to replace.

The **edits were identical by design**: phase 1 isolated the *discovery*
cost — context bytes, files read, queries. Correctness deltas were phase 2's
question. One-time index build was charged to the kern arm as setup and
reported, never hidden.

| task | arm | pass | files read | context bytes | queries |
|---|---|---|---|---|---|
| quoter-repeat-bug | with kern | true | 1 | 504 | 2 |
| quoter-repeat-bug | no index | true | 4 | 2196 | 4 |
| handler-legacy-validate | with kern | true | 1 | 483 | 2 |
| handler-legacy-validate | no index | true | 4 | 2196 | 4 |
| report-header-feature | with kern | true | 1 | 457 | 2 |
| report-header-feature | no index | true | 4 | 2196 | 4 |
| **totals** | with kern | 3/3 | 3 | 1444 | 6 |
| **totals** | no index | 3/3 | 12 | 6588 | 12 |

with-kern setup: one index build over the corpus = ~40 ms (amortized across
all tasks; per-task rows exclude it).
context delta: with kern reads **21.9%** of the bytes the no-index arm reads.

## Phase 2 — real-LLM A/B (dated protocol runs, never CI; harness deleted)

The reviewer's literal ask. The same tasks, but the AGENT chose its own
edits — `internal/coder`'s generate → apply → verify loop, verification
`build` + `test` where the test is the task's deterministic assertion gate.
The arms differed only in grounding:

- **with kern** — the `kern_search` response for the task's terms plus the
  one target file: one search, one read.
- **naive** — every corpus file concatenated: context stuffing, the only
  strategy without an index.

LLM runs were never deterministic and were never CI; the harness refused to
publish without a pinned model (`KERN_AGENT_EVAL_PUBLISH=1` with
`KERN_LLM_MODEL`).

### First run — 2026-10-08, N=5, default provider chain (resolved leg:
claude — the host-sampler; model not pinned, predates the publish gate),
rounds cap 3

| task | arm | pass | median rounds | median prompt tokens | median ms |
|---|---|---|---|---|---|
| quoter-repeat-bug | with kern | 5/5 | 1 | 518 | 8685 |
| quoter-repeat-bug | naive | 5/5 | 1 | 931 | 8266 |
| handler-legacy-validate | with kern | 5/5 | 1 | 468 | 8478 |
| handler-legacy-validate | naive | 5/5 | 1 | 929 | 8577 |
| report-header-feature | with kern | 5/5 | 1 | 479 | 8236 |
| report-header-feature | naive | 5/5 | 1 | 924 | 8216 |

**Reading (honestly):** both arms solved all 15 runs — at this corpus size
the task was easy enough that grounding did not separate pass rates. The
measured delta was **prompt cost: the kern arm medians at 468–518 tokens vs
naive's 924–931 — roughly half the prompt for the same outcome**, which is
phase 1's discovery economics showing up in a real LLM loop. Pass-rate
separation needs the harder protocol: larger corpora, round budgets of 1,
and noisy-repo distractors — future dated runs that never happened before
the harness was deleted.

## What a user should take from this

Phase 1 measured what kern is FOR: finding where to act without reading
everything. The corpus was deliberately small (a real repo amplifies the
no-index arm's cost linearly with repo size; the index arm stays targeted).
Phase 2 was designed to measure what that efficiency does to real agent
outcomes. Both harnesses are gone; the discovery-economics claim (targeted
reads vs full-corpus reads) is the part of this page that the live
`multi-repo-latency.md` and `cold-start.md` suites still substantiate.