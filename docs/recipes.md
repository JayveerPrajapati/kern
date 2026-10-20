# kern Recipes — Task-Oriented Guide

A set of concrete, task-oriented recipes for working with kern from the CLI.
Each recipe follows the same shape: **Goal** → **Command** → **What to look
for**. All commands below exist in the dispatch table (`cmd/kern/dispatch_table.go`)
— a parity test enforces this (`TestRecipesDocListsOnlyLiveCommands`); run
`kern --all` for the full catalog or `kern <cmd> --help` for one command's
options.

Two prerequisites for everything here:

```bash
kern index .        # build the symbol index for the current project
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
kern impact <symbol>                       # per-symbol dependents + covering tests
kern why <symbol>                          # what depends on the symbol
kern explore <symbol> --depth 0            # uncapped transitive blast radius
```

**What to look for:**

- `kern impact` prints the transitive blast radius and a risk rating. High risk
  or a wide radius → split the change or add tests first.
- The "Tests that cover it" section of `kern impact <symbol>` is your safety
  net inventory before the edit.
- Structural (AST-scoped) rename is not part of the CLI surface today; a plain
  rename should be followed by `kern index --force` and a
  `kern review` pass over the diff.

---

## 3. Review changes for risk

**Goal:** See what your working tree actually changes and whether it passes
the project's own gates.

```bash
kern review                     # changed files vs the index, with risk verdict
kern verify build,test,security,architecture,dependency
kern sec                        # security findings (regex + optional gosec)
```

**What to look for:**

- `kern review` links each changed file to affected symbols — read it before
  the diff. It exits 1 when changed files carry risk (the report is still on
  stdout).
- `kern verify <types>` accepts a comma-separated list of check types
  (`build,test,security,architecture,dependency,cve,license,secrets`) and
  returns a typed verdict per check. If `security` warns, run `kern sec` to see
  the findings.
- For a fast pre-commit-shaped pass: `kern verify --fast` scopes build+tests to
  uncommitted-changed packages only.

---

## 4. Audit who did what

**Goal:** Answer "who changed what, when, and why" for a file or area.

```bash
kern audit [--json]      # governance/decision audit trail
kern audit --json        # chain integrity: total entries, broken links, tiers
```

**What to look for:**

- `kern audit` surfaces the recorded decision/approval trail (approvals,
  authorized contexts, bypasses) — use it when you need provenance for a change.
- The chain summary (`broken`, `keyed` tier counts) is the tamper-evidence
  verdict; `kern evidence export` bundles it verifiably (recipe 10).
- For commit-level history use plain git (`git log --follow -- <file>`); kern
  adds the symbol-level and governance view.

---

## 5. Triage a crash or error

**Goal:** Map a stack trace or failing output to the symbols and code that
produced it.

```bash
kern probe "<what is the task?>"        # anchor a prose task to symbols
kern optimize --kind log "<stacktrace>"  # compress: keep errors + frames
kern why <symbol>                        # who depends on the failing symbol
kern explore <symbol>                    # the failing call neighborhood
```

**What to look for:**

- `kern probe` turns a vague description ("users can't log in after deploy")
  into concrete anchor symbols.
- `kern optimize --kind log` strips noise while preserving errors and stack
  frames — paste the compressed trace into your issue tracker.
- The deeper automated incident workflow (Alert → Correlate → Root Cause →
  Fix → Verify) is orchestrated through the SRE specialist role — reach it via
  `kern run "<intent>"` or the bundled skills (see `docs/skills.md`).

---

## 6. Check index health

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

## 7. Compress a file for an agent

**Goal:** Hand an LLM the essential slice of a file/log — not the whole thing.

```bash
kern compact <file>                      # symbol-aware compacted view of a file
kern optimize <prompt>                   # strip filler, mask secrets, keep code/paths
kern fit-context --file a.go,b.go --max-tokens 2000   # fit files to a budget
kern swap <doc.md> --max 4000             # budget-swap path-tagged fenced code blocks
```

**What to look for:**

- `kern compact` is the file-level tool: function signatures and key lines, not
  bodies. Pair it with `kern context <symbol>` for the surgical view.
- `kern optimize` reports tokens saved and masks secrets before anything leaves
  the machine; `-A`/`-B` keep context lines around matches.
- When the agent says "context too big", reach for `fit-context` with a number
  — it enforces the budget across files.

---

## 8. Search semantically

**Goal:** Find symbols by meaning and prose, not just exact names.

```bash
kern search "<query>"                    # ranked AST symbol search
kern search "<query>" --semantic         # semantic (embedding) mode
kern docs "<query>"                      # search indexed repo docs
kern probe "<natural language task>"     # map prose to anchor symbols
```

**What to look for:**

- Plain `kern search` is sub-millisecond and deterministic — start here.
- `--semantic` broadens recall when the name isn't obvious; it needs a local
  Ollama (the command warns and falls back to ranked search without one).
- `kern docs` searches only indexed repo docs — repos without a docs tree
  report that instead of a bare no-match.
- `kern probe` is the right tool when the query is a *task* ("Add returns wrong
  result"), not a name.

---

## 9. Check architecture drift

**Goal:** Verify the code still matches the intended layered architecture.

```bash
kern arch [--json]           # architecture summary: communities, hubs, coupling
kern guard check             # enforce boundary rules (inferred if unconfigured)
kern guard init              # scaffold .kern/boundaries.json from the codebase
kern doctor --arch-drift     # report drift against ARCHITECTURE.md ledger
```

**What to look for:**

- `kern arch` gives the overview (communities, hubs, cross-community edges);
  `kern guard check` fails on violations (reverse dependencies, layer skips).
  Without a boundaries file it infers defaults, so the guard is on by default.
- `--sarif` emits SARIF 2.1.0 if you need to feed findings into another tool.
- A clean `kern guard check` is the "no drift" signal; zero findings is the goal.

---

## 10. Ship an evidence bundle

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

## 11. Get the lay of the land

**Goal:** Orient in an unfamiliar repo in under a minute.

```bash
kern buddy                  # onboarding digest: conventions, layout, gotchas
kern project-map            # every source file with symbols + line counts
kern arch                   # subsystem communities, hubs, coupling
kern graph <symbol>         # call-graph neighborhood of a hub symbol
```

**What to look for:**

- `kern buddy` (the same engine the MCP `kern_buddy` tool serves) is the
  cheapest onboarding pass — index reads, no file walks.
- `kern project-map` shows where the weight is; `kern arch` reveals the coupling
  hot spots. Together they tell you where to read first and where refactoring
  will be painful.

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
- **Aliases.** `ask` = `meta`. Either spelling works.
- **Repeat reads.** Re-read the same file/symbol without paying for a re-render:
  `kern explore <symbol> --etag <E>` (and `context`/`compact`/`search`) — an
  unchanged target returns the tiny `unchanged (etag E)` marker instead of the
  full response.
