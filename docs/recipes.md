# kern Recipes — Task-Oriented Guide

A set of concrete, task-oriented recipes for working with kern from the CLI.
Each recipe follows the same shape: **Goal** → **Command** → **What to look
for**. All commands below exist in the dispatch table (`cmd/kern/dispatch_table.go`)
— run `kern guide` or `kern <cmd> --help` for the full option list.

Two prerequisites for everything here:

```bash
kern index .        # build the symbol index for the current project
kern watch .        # optional: keep it fresh while you work
```

If a command misbehaves on a stale index, re-run `kern index --force`.

---

## 1. Understand a symbol fast

**Goal:** Learn what a function/type does, who calls it, and who it calls —
without reading whole files.

```bash
kern explore <symbol>            # definition + callers + callees + blast radius
kern context <symbol>            # minimal source slice (definition + key lines)
kern graph <symbol>              # call graph around the symbol
kern why <symbol>                # rationale: dependents, usage, references
```

**What to look for:**

- `kern context` is the cheap first read — a few hundred tokens, not a file dump.
- `kern explore` answers "who depends on this?" in one shot; `--explain` adds
  plain-language reasoning.
- If the symbol isn't found, check the index is fresh (`kern doctor`), or search
  first (`kern search <name>`).

---

## 2. Is this refactor safe?

**Goal:** Estimate blast radius and dependencies *before* touching code.

```bash
kern impact "<describe the change>"        # blast radius + risk level
kern why <symbol>                          # what depends on the symbol
kern near <symbol>                         # structurally adjacent code
kern rename <old> <new> --apply            # structural rename (AST-scoped)
```

**What to look for:**

- `kern impact` prints the transitive blast radius and a risk rating. High risk
  or a wide radius → split the change or add tests first.
- `kern rename` without `--apply` is a **preview** — run it, read the affected
  sites, then apply. It is AST-scoped, so it rewrites call sites, not string
  matches.
- `kern what-if "<change>"` is an alias of `impact` if you prefer that phrasing.

---

## 3. Find dead code before deleting

**Goal:** Confirm a symbol really has no live callers before removing it.

```bash
kern dead [--json]        # symbols with zero references
kern larges [--json]      # biggest files/symbols by size
kern graph <symbol>       # verify: show callers before you trust `dead`
```

**What to look for:**

- `kern dead` lists candidates; cross-check the top ones with `kern graph
  <symbol>` — a symbol reached only from tests is still "dead" for production
  purposes, but a symbol with generated or reflection-based callers may be a
  false positive.
- `kern larges` helps you pick high-value cleanup targets (big, low-reference
  files).
- After deleting, re-run `kern dead` and `kern index --force` — the count
  should drop.

---

## 4. Review changes for risk

**Goal:** See what your working tree actually changes and whether it passes the
project's own gates.

```bash
kern changes                     # summarize uncommitted changes vs the index
kern review                      # alias of changes — same output
kern check                       # run governance gates (architecture, security, ...)
kern verify build,test,security,architecture,dependency
```

**What to look for:**

- `kern changes` links each changed file to affected symbols — read it before
  the diff.
- `kern check` returns PASS/BLOCK per gate; `kern check --ci` emits a machine
  readable JSON verdict and matching exit code (0 = passed, 1 = failed).
- `kern verify <types>` accepts a comma-separated list of check types
  (`build,test,security,architecture,dependency,cve,license,secrets`) and
  returns a typed verdict per check. If `security` blocks, run `kern sec` to see
  the findings.

---

## 5. Audit who did what

**Goal:** Answer "who changed what, when, and why" for a file or area.

```bash
kern audit [--json]      # governance/decision audit trail
kern churn [--json]      # which files change most often
```

**What to look for:**

- `kern audit` surfaces the recorded decision/approval trail (ADRs, approvals,
  authorized contexts) — use it when you need provenance for a change.
- `kern churn` ranks files by change frequency; high churn + high size
  (`kern larges`) is where bugs concentrate.
- For commit-level history use plain git (`git log --follow -- <file>`); kern
  adds the symbol-level and governance view.

---

## 6. Triage a prod crash

**Goal:** Map a stack trace or alert to the symbols and code that produced it.

```bash
kern trace <stacktrace-file>            # map a stack trace onto AST symbols
kern probe "<what is the task?>"        # anchor a prose task to symbols
kern incident <alert-json>              # run the incident pipeline (Workflow D)
kern runtime status                     # what production adapters are wired?
kern runtime drift                      # runtime routes vs code routes
```

**What to look for:**

- `kern trace` takes a file (or `-` for stdin) and resolves each frame to a
  symbol — the crash's call path becomes searchable and reviewable.
- `kern probe` turns a vague description ("users can't log in after deploy")
  into concrete anchor symbols.
- `kern incident <alert-json>` runs Alert → Correlate → Root Cause → Fix →
  Sandbox → Verify locally; production fixes stop at the approval gate by
  default. Feed it a snapshot: `kern incident <alert-json> <snapshot.json>`.

---

## 7. Correlate an alert to code

**Goal:** Tie telemetry/deployment evidence to the exact symbols involved.

```bash
kern correlate <alert-json> [--code]    # alert → service → deploy → commit → symbol
kern incident list                      # browse past incidents
```

**What to look for:**

- `kern correlate` builds the deep evidence chain; `--code` adds the
  incident→twin→code correlation section.
- The output ends at task/PR/agent links — that is the hand-off point for a fix.
- Everything runs locally off the runtime snapshot; no vendor calls.

---

## 8. Check index health

**Goal:** Confirm the binary, agent wiring, and index are all operational.

```bash
kern doctor                  # binary, agents, index freshness, capabilities
kern index --force           # rebuild the index from scratch
```

**What to look for:**

- `kern doctor` verdict line: "all systems operational" or a specific failing
  check. `[ok] freshness` shows symbol/edge counts — if stale, re-index.
- If searches return nothing obvious, `kern index --force` and retry before
  suspecting a query problem.

---

## 9. Compress a file for an agent

**Goal:** Hand an LLM the essential slice of a file/log — not the whole thing.

```bash
kern compact <file>                      # symbol-aware compacted view of a file
kern optimize <prompt>                   # strip filler, mask secrets, keep code/paths
kern terse "<text>"                      # aggressive single-pass compression
kern fit-context --file a.go,b.go --max-tokens 2000   # fit files to a budget
kern budget "<text>" --max 500           # trim text to a token budget
```

**What to look for:**

- `kern compact` is the file-level tool: function signatures and key lines, not
  bodies. Pair it with `kern context <symbol>` for the surgical view.
- `kern optimize` reports tokens saved and masks secrets before anything leaves
  the machine; `-A`/`-B` keep context lines around matches.
- When the agent says "context too big", reach for `fit-context` with a number
  — it enforces the budget across files.

---

## 10. Search semantically

**Goal:** Find symbols by meaning and prose, not just exact names.

```bash
kern search "<query>"                    # ranked AST symbol search
kern search "<query>" --semantic         # semantic (embedding) mode
kern doc-search "<query>"                # search bundled/processed docs
kern probe "<natural language task>"     # map prose to anchor symbols
```

**What to look for:**

- Plain `kern search` is sub-millisecond and deterministic — start here.
- `--semantic` and `doc-search` broaden recall when the name isn't obvious.
- `kern probe` is the right tool when the query is a *task* ("Add returns wrong
  result"), not a name.

---

## 11. Check architecture drift

**Goal:** Verify the code still matches the intended layered architecture.

```bash
kern arch [--json]           # architecture summary: layers, edges, violations
kern guard check             # enforce boundary rules (inferred if unconfigured)
kern guard init              # scaffold .kern/boundaries.json from the codebase
kern doctor --arch-drift     # report drift against ARCHITECTURE.md ledger
```

**What to look for:**

- `kern arch` gives the overview; `kern guard check` fails on violations
  (reverse dependencies, layer skips). Without a boundaries file it infers
  defaults, so the guard is on by default.
- `--sarif` emits SARIF 2.1.0 if you need to feed findings into another tool.
- A clean `kern guard check` is the "no drift" signal; zero findings is the goal.

---

## 12. Find test gaps

**Goal:** Know which symbols are exercised and which are not.

```bash
kern test-gaps [--json]      # untested / thinly-tested symbols
```

**What to look for:**

- Focus on gaps in symbols you are about to change (cross-reference with
  `kern impact` output). `testgaps` is an alias with identical behavior.
- After adding tests, re-run and watch the gap list shrink — that is the
  measurable definition of "better covered".

---

## 13. Find duplication

**Goal:** Locate structurally duplicated code before refactoring.

```bash
kern twin [--root .]         # structural duplication report
```

**What to look for:**

- `kern twin` finds near-identical structures across files/languages. Each
  cluster is a consolidation candidate — but check `kern why <symbol>` on each
  twin before merging: "keep first occurrence" contracts exist for a reason.
- Pair with `kern larges` to prioritize: big duplicated files first.

---

## 14. Ship an evidence bundle

**Goal:** Produce a verifiable record of what the repo claims (index, gates,
audit chain) and let anyone check it later.

```bash
kern evidence export [--sign]                # build a signed evidence bundle
kern evidence verify --file bundle.json      # verify seal + audit chain
kern evidence explain --file bundle.json     # plain-language explanation
kern anchor <anchor-id>                      # resolve a specific evidence anchor
```

**What to look for:**

- `export` bundles the evidence store with per-record sha256 checksums and a
  bundle digest; `--sign` adds a project key signature (`.kern/keys/`, created
  on first use).
- `verify` recomputes every checksum before any trust — a tampered bundle fails
  cleanly. `--expect-fingerprint` pins the signer as a trust anchor.
- Include the bundle in a PR/incident report so the claim "these checks passed"
  can be independently re-verified.

---

## 15. Get the lay of the land

**Goal:** Orient in an unfamiliar repo in under a minute.

```bash
kern entries                # entry points (aliases: entry-points)
kern larges                 # biggest files
kern cycles                 # circular dependency clusters
kern communities            # coupled groups of symbols
kern flows                  # symbol flow between layers
kern bridges                # coupling points between subsystems
```

**What to look for:**

- `kern entries` shows where execution starts; `kern larges` shows where the
  weight is; `kern cycles` and `kern communities` reveal the coupling hot
  spots. Together they tell you where to read first and where refactoring will
  be painful.
- This is the cheapest possible onboarding pass — all index reads, no file
  walks.

---

## Notes

- **Index first.** Every recipe assumes a built index (`kern index .`). `kern
  doctor` verifies freshness.
- **Flags.** These recipes use the core forms. Append `--json` to any command
  that supports it for machine-readable output; `--root` points at another
  project root.
- **MCP equivalents.** Every CLI command here has an MCP tool counterpart
  (`kern_*`) — see `docs/tool-catalog.md`. `kern_meta` routes natural-language
  requests to the right tool automatically.
- **Aliases.** `review` = `changes`, `what-if` = `impact`, `testgaps` =
  `test-gaps`, `entries` = `entry-points`, `kernops` = `ops`. Either spelling
  works.