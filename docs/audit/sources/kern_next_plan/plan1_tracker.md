# plan1.txt — Deep Verification & Implementation Tracker

**Source plan:** `plan1.txt` (same directory) — "Next-gen features for JayveerPrajapati/kern" (14 proposed features across 5 sections)
**Verified repo:** `/Users/jayveer.prajapati/ai_workspace/kern_opensource/kern`
**Verification date:** 2026-09-01
**HEAD:** `63548ed` (2026-09-01) — clean tree
**Method:** 5 parallel source-level audit lanes (one per plan section); every verdict backed by `file:line` evidence read from source. No speculation.

## Verdict legend

- **ALIGNED** — kern already implements what the plan proposes (no work needed, maybe polish)
- **PARTIAL** — kern has a related capability but is missing a key piece the plan describes
- **MISSING** — not implemented anywhere

## Summary table

| # | Plan proposal | Verdict | Key evidence | Gap vs plan |
|---|---|---|---|---|
| 1a | AST skeleton pruning in budget fitting | **PARTIAL** | `internal/code/fold.go:69-152`, `swap.go:27-56`, `compact_file` (handlers_context.go:38-64) | `budget.Fit` (budget.go:16) is line-based, not AST-aware; folding covers ~8 of 17 langs |
| 1b | Semantic log clustering (MinHash/Levenshtein, "Repeated Nx") | **MISSING** | `compress.go:35-84` exact-line dedup only | No edit-distance/minhash, no hex-pointer/ID stripping, no repetition counting |
| 1c | Dynamic diff compaction (semantic unchanged blocks) | **PARTIAL** | `diff.go:81-120` unified diff; `hooks.go:93-118` drops context lines | No "// … N lines unchanged in X" semantic blocks |
| 2a | Regression calibration (F1 vs git history) | **ALIGNED** | `evaluate/calibration/main.go:70-117` (Impact F1, git ground truth) | Only gap: not a `kern calibrate` subcommand |
| 2b | What-if blast-radius simulation | **PARTIAL** | `whatif.go:90-190` + CLI/MCP wired | Broken call sites not flagged; test gaps not highlighted; boundary analysis only via firewall denials |
| 2c | LSP-driven token masking | **MISSING** | no LSP code anywhere; `verify.go:64-97` is text-ref check | Entire capability absent (heavier cousin exists: `kern_verify_output`) |
| 3a | Sandbox impact manifest (FS/network activity trail) | **PARTIAL** | `sandbox.go:149,254` snapshot/restore; `audit.go:235` SHA-256 chain | Audit chain is tool/action granularity, not file/syscall level |
| 3b | Declarative assertions (@pure, layer isolation) | **PARTIAL** | `architecture/config.go` from/to + layer rules (`engine.go:116`) | No purity/mutation assertions anywhere |
| 3c | Automated security test generation | **MISSING** | `sec.go:128` regex scan; `coverage.go:105` coverage only | No taint-flow analysis, no test generation |
| 4a | Concurrent index sharding | **MISSING** | `engine.go:336` serial `filepath.WalkDir`, zero goroutines | Whole build path is single-threaded |
| 4b | SQLite WAL ring buffering (hot/cold tiers) | **PARTIAL** | `sqlite_store.go:51-65,127` WAL+FTS5 | Plain full mirror, DELETE+re-insert; no TTL/tiering/ring |
| 4c | Dependency-aware watch debounce | **PARTIAL** | `watch_native.go:82`, `watch.go:150-160` timer debounce | Not dependency-aware; re-index is full-tree rebuild |
| 5a | Distributed lock sync (kern lock/unlock) | **ALIGNED** | `lock_unix.go:14-34` flock; CLI+MCP both wired | None (advisory, non-blocking by design) |
| 5b | Agent-to-agent event telemetry | **PARTIAL** | `eventbus.go:239-296`; `verification/engine.go:530-547`; `webhook.go:148-170` | `kern guard` CLI doesn't publish; engine bus not bridged in kern-server; in-process only |

**Tally: 2 ALIGNED · 8 PARTIAL · 4 MISSING**

## Final status — ALL 12 backlog items CLOSED (2026-09-01)

Every implementable item in the backlog is implemented, tested, and verified; the two ALIGNED items (2a calibration, 5a locks) needed only the calibrate CLI wrapper (done) and no action respectively. 4b (SQLite tiering) remains SKIP by design. Whole-tree gate: `go build ./...` OK, `go vet ./...` clean, `go test -short ./... ./cmd/...` → **86 packages ok, 0 FAIL**. All changes are UNCOMMITTED in one working tree.

Remaining follow-ups (tracked, not blockers):
- CLI `kern sandbox` does not render the impact manifest (MCP surface does) — one-line render add if wanted.
- Partial re-index (dependency-ordered dirty-file rebuild) — the big 4c follow-up; freshness invariants need care.
- Fold coverage beyond the current ~8 languages (yaml/css/html pass through) — 1a follow-up.
- Full LSP bridge (2c's lighter alternative shipped instead).

Observation #4 (audit tamper-chain warning) — **CLOSED** (2026-09-01). Root cause: cross-process writers chained from stale in-memory heads — `AppendExternal`'s self-heal only fired on a completely empty log; the repo's entry 237 (Blueprint append) chained from a head that predated entry 236. Fix: `WithLockPath` blocking advisory flock (darwin/linux flock, windows exclusive-create) + `storedEntriesLocked`/tail-refresh before EVERY persisted write (Record + AppendExternal); `RepairChain()` + `kern audit repair [root]` (explicit, re-chains from first broken link, content preserved); wired into platform.go:158 + runAuditAppend. Tests: 5 new (StaleHead regression, AppendExternalStaleHead, ConcurrentWriters 2×15, RepairChain, LegacyNoLock) + pre-existing PASS, `-race` clean, cross-compiles darwin/linux/windows. Real store: repaired (473 entries re-chained from break 237), `kern audit` clean, second repair → "already verified", 711 entries. Uncommitted.

---

## Progress log (one-by-one execution)

| Item | Status | Date | Evidence |
|---|---|---|---|
| 1b Semantic log clustering | **CLOSED** | 2026-09-01 | `compress.go`: `Options.Cluster` + `normalizeForCluster` + `clusterLines` + `levenshteinRatio`; enabled by default at all 3 `optimize.Log` call sites (covers MCP `kern_optimize_log` + CLI `kern optimize log`). 6 new tests PASS (RepeatedTraces, Disabled, Deterministic, FuzzyMerge, RespectsMaxLines, KeepsUniqueErrors) + 5 pre-existing PASS; `go build ./...` OK; `go vet` clean. Note: fuzzy merge ratio 0.85 (spec's alice/bob test needs ≥0.857 — 0.9 unsatisfiable; guards: same first token + ≤30% length diff). Uncommitted. |
| 4a Parallel index build | **CLOSED** | 2026-09-01 | `engine.go`: `Build` → dispatcher (`KERN_INDEX_SERIAL=1` forces serial); `buildSerial` = original body verbatim; `buildParallel` = serial walk collecting jobs → `GOMAXPROCS(0)` worker pool (pure workers: ReadFile/isIndexable/computeFileResult) → main-goroutine ordered merge by seq → same finalize passes. `addFile` split into pure `computeFileResult` + `applyFileResult` (hash recorded before parse — staleness invariant kept). `parallelMinJobs=256` fallback: below it, serial apply (fixer's 0.94x small-fixture regression eliminated: 85.3ms bench). Tests: `parallel_test.go` (MatchesSerial byte-identical, SkipSemantics, DeterministicRepeat) — all PASS; `-race` clean; full `go test ./internal/index/` PASS. Real-repo A/B (kern itself, ~480 files): warm parallel 2.06s vs serial 2.24s (~8%); speedup grows with repo size. Uncommitted. |
| 2b What-if enhancements | **CLOSED** | 2026-09-01 | `whatif.go`: Impact gains `BrokenCallSites` + `UntestedAffected` (omitempty). ChangeSignature/RenameSymbol → direct `calls`-edge callers (deduped/sorted) + INFERENCE claim; every kind → `WhatTestsCover` scan over target+callers (cap 25) → untested list + claim. `platform.go populateWhatIfEvidence`: `intel.CheckBoundaries(p.ix, b, imp.Files)` violations appended to ArchitectureViolations (deduped, fail-open without boundaries.json). `app/render.go`: text report prints both lines (CLI+MCP). Tests: 5 whatif + 1 app boundary test, ALL PASS; `go vet` clean; live CLI: `kern what-if compress.CompressLog change_signature` → `Broken call sites: Log, promptUncached` + INFERENCE claim, existing fields intact. Web = raw JSON, no change needed. Uncommitted. |
| 1a AST-aware `budget.Fit` | **CLOSED** | 2026-09-01 | New `code.DetectExtFromContent` + `code.FoldContent` (conservative marker sniffing, first 200 lines, priority-resolved; unknown → unchanged) and `budget.FitCode` (fold first, then existing Fit; hard budget guarantee inherited). Wired into `kern_context_budget` MCP tool (handlers_optimize.go:216) + `kern budget` CLI (cmd_optimize.go:196) only — intel/context/swap/pack callers untouched. Tests: 6 code + 5 budget, ALL PASS; build/vet clean. Live: Go file @ 36 tokens → 18 tokens, `func main() {` + `// ... body elided: 6 lines ...`; non-code byte-identical to Fit. Uncommitted. |
| 1c Diff compaction | **CLOSED** | 2026-09-01 | `diff.Compact` (view-only: context runs >2 lines → ` ... N lines unchanged in <span> ...`; changed lines verbatim; nil resolver ok) + `diff.IndexSpanResolver` (index Symbol Line/End spans, skip End==0). MCP `kern_diff_files` gains `compact` arg (loadIndex, error→nil); CLI `kern udiff --compact` (rejects `--out`, loads `index.Load(root)`). Tests: 6 diff + 1 MCP, ALL PASS; build/vet clean. Live: `udiff --compact` → `... 11 lines unchanged ...` + `... 8 lines unchanged in computeSomething ...`; non-compact patch intact; `--compact --out` exit 2. Uncommitted. |
| 3b `@pure` assertions | **CLOSED** | 2026-09-01 | `intel.CheckPurity(ix, files)` (new purity.go): @pure-annotated Go funcs (doc-comment marker) flagged for pkg-var writes, receiver-field writes, pointer-param/receiver mutation, channel sends; locals shadow correctly; non-Go/unparseable skipped; deterministic. Opt-in via `"pure": true` in boundaries.json (`Boundaries.Pure`). Wired into `kern guard check` (runGuard) + MCP `kern_guard_check` — same Violation struct/render/exit-2 path. Tests: 11 purity + MCP Guard + CLI, ALL PASS; build/vet clean. Live: `@pure` + `counter++` → REJECT `@pure → var:counter` exit 2; fixed → PASS; flag off → PASS. Uncommitted. |
| 5b Event telemetry wiring | **CLOSED** | 2026-09-01 | `publishGuardEvents` in runGuard → `eventbus.New()` + `EnablePersistence(.kern/events.jsonl)`: 1 ArchitectureViolation per guard violation (before exit panic, so REJECT still persists) + ArchitectureWarning when boundaries unconfigured. `web.New`: `a.bus.Replay(...)` (BEFORE EnablePersistence — prevents re-append on restart), `EnablePersistence(same path)`, `platform.WithBus(a.bus)` — verification-engine events now reach the webhook bus. `Platform.Bus()` getter already existed. Tests: 2 CLI guard + 1 web bridge, ALL PASS; build/vet clean. Live: violating guard → exit 2 + `architecture.violation` event persisted; reruns append; kern-server restart smoke clean. Uncommitted. |
| 4c Watch debounce | **CLOSED** | 2026-09-01 | `watch.go`/`watch_native.go`/`fsevents.go`/inotify/kqueue: event channel now `chan string` (rel paths); ALL debounce paths trailing (reset per event; also fixes latent single-fire bug in old debounceSend); `watch_deps.go` pure helpers `relatedFile` (same non-root dir OR same stem w/ different base) + `shouldExtendDependency`; fire re-arms up to 3× (seq-generation guard, deadlock-free) while related files keep changing; `watchDebounce` package knob (150/300ms prod values preserved). Poll ticker + rebuild/diff logic untouched. Tests: 5 new (deps ×2, trailing, related-burst merge, unrelated-separate) + all pre-existing PASS, `-race` clean. Live smoke: foo.go+foo_test.go → ONE rebuild with both files; a.go then b.go 1.2s apart → two. Uncommitted. |
| 2a `kern calibrate` CLI | **CLOSED** | 2026-09-01 | New `internal/calibrate` (logic moved verbatim from evaluate/calibration/main.go; `Run(root, commitsN, thresholds, w)`; helpers kept unexported; errors returned instead of os.Exit(1)); evaluate/calibration/main.go = thin main (identical flags); `kern calibrate [root] [--commits N] [--thresholds a,b,c]` registered in dispatch + shared flags struct (commits=60, thresholds default). Tests: 2 calibrate + 1 CLI, ALL PASS; build/vet clean. Output byte-identical to old harness (diffed). Live: `kern calibrate . --commits 5` → both tables. Uncommitted. |
| 2c Draft-code validation | **CLOSED** | 2026-09-01 | `verify.CheckDraft(ix, root, code, lang)` (new draft.go): Go parse-error finding (stops), relative-import existence under root, unknown_symbol for simple calls (builtins/locals/index checked), unknown_method for package-alias selectors on known-indexed packages; non-Go → nil; deterministic. MCP `kern_check_draft` (handlers_security + server.go 3 registration sites) + `kern check-draft` CLI shim (forced by 3 parity invariants: PluginMatchesMCPCatalog, PluginSubcommandsReachCLI, MCPToolsReachableFromCLI) + BOTH plugin files updated (byte-identical). Tests: 8 verify + 2 MCP + parity all PASS; build/vet clean. Live: `totallyMissingFunc()` → `unknown_symbol` finding; clean draft → OK. User-level plugin copies synced (~/.opencode + ~/.config/opencode per memory #167). Uncommitted. |
| 3a Sandbox change manifest | **CLOSED** | 2026-09-01 | `Snap.Manifest() ([]Change, int)` (sandbox.go): created/modified/deleted vs snapshot copies (path, size, sha256; skip-cap/read-error paths excluded; sorted) — computed in `Run` BEFORE the restore decision so failed runs' impact stays auditable; `Result.Manifest`; `kern_sandbox` MCP handler renders `=== sandbox impact manifest ===` section (+/-/~ lines + summary with skip count + "tree restored — changes rolled back"). Output byte-identical when manifest empty. Tests: 5 sandbox + 1 MCP, ALL PASS; build/vet clean. Live: `touch created.go && echo y >> existing.go` → `+ created.go (0 B, sha256:...)`, `~ existing.go (12 B, ...)`, `2 change(s)`; failure+rollback case also recorded. Note: CLI `kern sandbox` doesn't render the manifest (MCP is the plan's surface) — possible follow-up. Uncommitted. |
| 3c Taint-lite + scaffolds | **CLOSED** | 2026-09-01 | `sec.TaintLite(ix, findings)`: containing-function via innermost span (Kind func/method), entry set from `Symbol.Entry` (bare+qualified), bounded deterministic BFS over `Callers` (depth ≤10, ≤500 visited, bare/qualified keys per graph quirk, sink-as-entry trivially tainted), 8 source-expression file patterns fallback → `TaintFinding{Func, Tainted, EntryPoint, Path}`. `sec.GenTestScaffold`: package clause from sink file, RuleCamel naming, per-family probe comments, `TestTaint<Rule><Line>` frame with TODO body, `# write to: <base>_taint_test.go`. MCP `kern_taint` (root/file/generate) + CLI `kern taint` (+ fixed pre-existing 2c dispatch tab bug) + both plugin files updated (byte-identical, user-level copies re-synced). Tests: 10 sec + 2 MCP + 3 parity, ALL PASS; build/vet clean. Live: `db.QueryRow("SELECT ... " + id)` in net/http handler → `tainted: yes` + scaffold. Uncommitted. |
| 4b SQLite tiering | **SKIP** | — | No measured problem (see §4b) |

---

## Section 1 — Advanced Static Compression

### 1a. AST skeleton pruning — PARTIAL

**What exists:**
- `internal/code/fold.go:69-152` — `Fold`/`RenderTier` with `TierFolded` replaces function/method body interiors with `// ... body elided: N lines ...` while keeping signatures, types, consts, package/import headers verbatim.
- `internal/code/summarize.go:140` — `Summarize` produces signature-only summaries (`path + kind:name:line:params`).
- `internal/swap/swap.go:27-56` — `SummaryMode`/`Fit` swap fenced `lang:path` code blocks to signature summaries to fit a budget.
- `internal/mcp/handlers_context.go:38-64` — `kern_compact_file` defaults to `TierSummary`, supports `tier=folded`.

**Gap vs plan:**
1. The dedicated budget fitter `internal/budget/budget.go:16` (`budget.Fit` → `kern_context_budget`, handlers_optimize.go:202-215) is **generic line-based**: exact-line dedup, keep head + "important" lines, truncate. Zero AST awareness — it does not preserve signatures or drop bodies.
2. Folding covers **~8 of 17 detected languages**: Go (go/ast), js/java/c/rust/shell (brace-fold), python/ruby (indent-fold); yaml/css/html pass through unfolded (fold.go:94,180-196).

**Action (P1, effort S-M):** make `budget.Fit` AST-aware by reusing `code.Fold` tiers (Go + already-foldable langs first); extend fold to remaining 17 langs as a follow-up.

### 1b. Semantic log clustering — MISSING ⭐ highest-value gap

**What exists:** `internal/compress/compress.go:35-84` `CompressLog` — strips leading timestamp via regex (compress.go:13,56), then **exact-line dedup** (`seen map[string]bool`, compress.go:73-76). `internal/optimize/optimize.go:245-265` `Log` just wraps it. `internal/terse/` strips prose filler, not clustering.

**Gap vs plan (entirely absent):**
- No Levenshtein/MinHash/edit-distance anywhere (only `semcache.Similarity` — Jaccard over word shingles — used for caching prior queries, semcache.go:144, never for in-document clustering).
- No hex-pointer/UUID/numeric-ID stripping before dedup (only timestamps).
- No repetition counting — no `--- [Repeated 450x] ConnTimeout ... ---` output; only `… (N lines omitted)` truncation cap (compress.go:79-81).

**Action (P0, effort S-M):** add a deterministic similarity-clustering pass: strip volatile tokens (hex pointers, UUIDs, goroutine IDs, timestamps), cluster near-identical lines via MinHash/LSH or normalized-Levenshtein bucket, emit `--- [Repeated Nx] <representative> ---`. Pure stdlib, no LLM; slots directly into `compress.CompressLog` behind `optimize.Log`; measurable via `evaluate/bench`.

### 1c. Dynamic diff compaction — PARTIAL

**What exists:** `internal/diff/diff.go:81-123` `Unified` — standard LCS unified diff with `@@` hunks, 3 context lines, git merge rule. Exposed as `kern_diff_files` (handlers_exec.go:76-93). `internal/hooks/hooks.go:93-118` `compressDiff` (post-commit memory hook) **drops all unchanged context lines** entirely.

**Gap vs plan:** nothing summarizes unchanged context into semantic blocks (`// ... 140 lines unchanged in UserManager`). `kern_diff_files` keeps raw context lines; `hooks.compressDiff` deletes them without summary.

**Action (P1, effort S-M):** new compaction mode in `internal/diff` (view-only, must NOT break patch applicability): collapse unchanged runs into annotated blocks using symbol spans from the index. Expose as `kern_diff_files --compact` or a `kern udiff` command.

---

## Section 2 — Next-Generation Local Code-Intelligence

### 2a. Regression calibration — ALIGNED

**What exists:** `evaluate/calibration/main.go` — runnable calibration harness: table 2 computes **Impact F1** — "given the symbols a commit touched, the graph predicts affected files (blast radius); ground truth = files the commit actually edited; precision/recall/F1" (main.go:70-75,109-117). Uses real git history: `git rev-list --max-count N HEAD`, per-commit `intel.FilesForRangeL` + `AnalyzeChangesRanged` + `BlastRadius` + `AffectedFiles` (main.go:93-94). Documented in `evaluate/calibration/README.md` (sample: precision=0.235 recall=1.000 F1=0.381).

**Gap vs plan:** trivial — not exposed as a `kern calibrate` CLI subcommand (runs via `go run ./evaluate/calibration`).

**Action (P2, effort XS):** wrap as `kern calibration` subcommand (or `kern calibrate` alias) calling the same code.

### 2b. What-if blast-radius simulation — PARTIAL

**What exists:** `internal/whatif/whatif.go:90-190` `Simulate(g, Change)` — applies hypothetical change (RemoveSymbol/ChangeSignature/RenameSymbol/…) to an in-memory graph copy; returns Impact with Affected/Files/Services/Tests/Databases/Risk/Recommendation/Alternatives/Mitigations/Confidence + typed `domain.Claim`s (RECOMMENDATION etc.). Exposed: CLI `kern what-if` (dispatch.go:80, cmd_review.go:150-176) + MCP `kern_what_if`/`kern_impact` (server.go:1056-1067, handlers_highlevel.go:183).

**Gap vs plan (3 specific gaps):**
1. **Broken call sites not flagged** — for ChangeSignature/RenameSymbol, affected = callers (whatif.go:164) but no marker distinguishing callers that would fail to compile.
2. **Test gaps not highlighted** — `WhatTestsCover` (whatif.go:186-188) lists tests that *cover* the affected symbols; missing tests are not reported (`kern_test_gaps` exists separately, internal/intel/coverage.go:105).
3. **Boundary violations only via firewall denials** — `ArchitectureViolations` defaults to empty (whatif.go:216); populated only when `p.fw.Check("whatif", target, "modify")` denies (app/platform.go:334-336), not a general graph boundary analysis.

**Action (P1, effort M):** (1) mark callers whose signature/name changes break them (compile-impact heuristic: same-package call sites, interface impls via `kern_inherits` data); (2) diff `WhatTestsCover` against affected → emit missing-test list; (3) run `intel.CheckBoundaries` on the hypothetical graph and surface violations directly.

### 2c. LSP-driven token masking — MISSING

**What exists:** nothing LSP — grep for `textDocument`/`gopls`/jsonrpc-initialize returns only MCP protocol code (cmd_setup.go:150-187, kern-mcp/main.go:55). `kern_verify_output` (internal/verify/verify.go:64-97) extracts `file:line`, symbol-name, and route refs from an output string via regex and checks them against index/source — a post-hoc text hallucination check, **not** draft-code validation.

**Gap vs plan:** the entire capability — real-time context tracking, forcing agent autocomplete within resolved symbols, hard validation error for non-existent/un-imported types — is absent.

**Action (P2, effort L — recommend a lighter alternative):** embedding real LSP needs external language servers (violates zero-dep spirit). Better: an MCP tool `kern_check_draft` that parses agent draft code (Go via go/ast; others via existing heuristics) and validates referenced symbols/imports against the index symbol table, returning typed errors. Reuses `verify.go` primitives.

---

## Section 3 — Ironclad Enterprise Governance & Security

### 3a. Sandbox impact manifest — PARTIAL

**What exists:** `internal/sandbox/sandbox.go:149` `Snapshot` (full-tree clone for rollback) + `:254` `Restore` — snapshot-before/restore-after only. Governance audit trail: `internal/governance/audit.go:235` `computeAuditHash` builds a **SHA-256 hash chain** (each entry linked via prevHash, tamper-evident, `VerifyChain`); entries written from `Firewall.Check` (firewall.go:124) at **tool/action granularity** (`AgentID/Action/Resource/Risk/Result`).

**Gap vs plan:** no per-operation (file read/write/network) recording; audit chain granularity is governance decisions, not syscalls/files.

**Action (P2, effort M — optional):** syscall-level tracing is not portable in stdlib. Feasible middle ground: sandbox writes a post-run **change manifest** (files created/modified/deleted vs snapshot, sizes, hashes) as an auditable artifact appended to the hash chain — same tamper-evidence, stdlib-only. Full syscall manifest: skip unless a real requirement appears.

### 3b. Declarative architectural assertions — PARTIAL

**What exists:** `internal/architecture/config.go` — `Rule{From,To,Action: forbid|allow}` + `LayerFrom/LayerTo` + per-layer `Depends`; `engine.go:35` delegates from/to rules to `intel.CheckBoundaries`, `engine.go:116` `layerChecks` evaluates layer constraints over the call graph. So **layer isolation and allow/forbid rules ARE supported**.

**Gap vs plan:** **purity/mutation assertions are entirely absent** — no `@pure`/mutability annotation handling anywhere (grep clean). `internal/consistency/` is a cross-source knowledge-consistency checker (fingerprints across GRAPH/TWIN/MEMORY/GIT/RUNTIME/ARCHITECTURE/TESTS), unrelated.

**Action (P1, effort M):** extend boundaries/config with `purity` assertions: AST-check marked functions for assignments/pointer-escape in scope (Go first via go/ast; others later); violations fail `kern guard check` with exit code 2, same path as import violations.

### 3c. Automated security test generation — MISSING

**What exists:** `internal/sec/sec.go:128` `ScanFile` — regex/pattern static detection (hardcoded secrets, sql-injection, command-injection, unsafe-deserialization, code-eval, weak-crypto, insecure-random, config rules). **No taint/data-flow analysis** (grep for taint/sink clean). `internal/intel/coverage.go:105` `TestGaps` measures coverage gaps only. No test-file generation anywhere (only test fixtures in `_test.go` files).

**Gap vs plan:** entire capability absent — no taint-flow analysis, no pytest/go-test script generation for sinks.

**Action (P2, effort L — LLM-assisted, per north-star principle #3):** build deterministic half first: `internal/sec` taint-lite (source→sink reachability over the call graph for injection families) + test-scaffold generation (correct imports/setup, `func TestXxx(t *testing.T)` frames targeting flagged file:line). Let the LLM fill probe bodies via `internal/llm`; deterministic parts (scaffold, naming, wiring, validation) stay rule-based.

---

## Section 4 — Zero-Dependency Performance & Scaling

### 4a. Concurrent index sharding — MISSING ⭐ best perf win

**What exists:** `internal/index/engine.go:336` `Build` — single serial `filepath.WalkDir`; each file read/parsed/appended synchronously (engine.go:451 `addFile`, :463-477 extract, :514 `computeCallers`, :563 `addDispatchEdges`, :288 `reindexByFile`). Zero goroutines/errgroup/NumCPU in the build path (only project watcher has goroutines). Determinism today comes from lexical walk order, not explicit sorting.

**Gap vs plan:** build is fully single-threaded. No per-directory/package sharding.

**Action (P0, effort M):** parallelize the parse phase with a bounded worker pool (parse/analyze per file in parallel), keep merge phases (`computeCallers`, `addDispatchEdges`, `resolveEntries`) serial, and add an explicit deterministic sort before emit (required: byte-identical output for freshness/hash proofs). Benchmark with `BenchmarkIndexBuild`/`BenchmarkIndexBuildLarge` (bench_test.go:57,76).

### 4b. SQLite WAL ring buffering — PARTIAL (recommend: skip)

**What exists:** `internal/index/sqlite_store.go:51-65` — WAL mode + busy_timeout + synchronous=NORMAL; `:127` FTS5 `symbols_fts` virtual table (unicode61). Store is a **plain full persistent mirror**: `Save` does DELETE-all + re-insert every row (no TTL, no tiering).

**Gap vs plan:** sliding TTL window / hot-in-WAL-cold-in-JSON delta tiering absent.

**Action (P3, effort L — **recommend SKIP**): no measured problem. Local single-user tool; full mirror in WAL is fast and simple. Profile first (`BenchmarkIndexBuildLarge`, `kern stats`); only build tiering if a real slow-path appears.

### 4c. Dependency-aware watch debounce — PARTIAL

**What exists:** `internal/project/watch_native.go:82` `debounceSend(notify, 200ms)` (trailing timer); `watch.go:150-160` main loop uses `time.AfterFunc(150-300ms, rebuild)` with leading-edge window; `fsevents.go:106-108` external-watcher path trailing 200ms reset. Rebuild = `index.FileHashes` → `index.Diff` → **full `index.Build` + Save** (index/watch.go).

**Gap vs plan:** debounce is a plain event-burst timer — no knowledge of file-to-file dependencies; re-index is a full-tree rebuild, not dependency-ordered partial re-index.

**Action (P1, effort M):** (1) cheap win — make debounce trailing for all paths (already exists in native path; unify); (2) medium — dependency-aware wait: hold rebuild while recently-touched files' co-change partners (from `index.Diff` dirty set) are still receiving events; (3) bigger — partial re-index of dirty files + their reverse-dependents via `reindexByFile` instead of full `Build`. (3) is the real payoff but touches freshness invariants — do (1)+(2) first.

---

## Section 5 — Multi-Agent Coordination

### 5a. Distributed lock sync — ALIGNED ✅

**What exists:** `internal/lock/lock_unix.go:14-34` — `syscall.Flock(LOCK_EX|LOCK_NB)`, non-blocking, returns `ErrLocked` (lock.go:30) when held; marker files in `.kern/locks/<scope>.lock` shared by all agents on the workspace (lock.go:22); `Held/List` report scope/PID/acquired_at. Dual surface: CLI `kern lock/unlock/status` (cmd_context.go:244-300) + MCP `kern_lock/kern_unlock/kern_lock_status` (server.go:872-891, handlers_governance.go:39-115). OS auto-releases on process exit (no deadlock).

**Gap vs plan:** none. Note: advisory + error-on-contention (non-blocking) by design.

**Action:** none.

### 5b. Agent-to-agent event telemetry — PARTIAL

**What exists:** `internal/eventbus/eventbus.go:239-296` — in-process pub/sub (bounded history, idempotency, dead-letter, persisted replay); Kind taxonomy includes `ArchitectureViolation`/`ArchitectureWarning` (eventbus.go:32-48). `internal/verification/engine.go:530-547` `VerifyArchitecture` publishes one ArchitectureViolation per violation (+ warning) **when bus is non-nil** (`WithBus`). External delivery: `internal/webhook/webhook.go:148-170` POSTs events as JSON; `cmd/kern-server/main.go:89-99` subscribes whole bus → webhook delivery (`KERN_WEBHOOKS`). `AgentHandoff` events for in-pipeline stage handoffs (agents/pipeline.go:134-137).

**Gap vs plan (3 wiring gaps, all small):**
1. **`kern guard` CLI never publishes** — runGuard (cmd_context.go) only renders verdicts; events exist only via the verification engine.
2. **kern-server bridge missing** — `web.New` builds platform via `app.NewWithGraph` with no `WithBus` (web.go:111-125), so the webhook-subscribed `a.bus` never receives verification/architecture events.
3. **No cross-process alerting** — bus is strictly in-process (eventbus.go:1-7); no persistent queue/block for a separate developer agent.

**Action (P1, effort S-M):** (1) publish ArchitectureViolation from runGuard; (2) pass `WithBus(a.bus)` in web.New (one-line bridge); (3) optional — eventbus JSONL persistence (replay exists) so a second process can tail/alert. Together these deliver the plan's "auditor flags → developer alerted" flow.

---

## Backlog (ranked by value/effort)

| Priority | Item | Type | Effort | Why |
|---|---|---|---|---|
| **P0** | 1b Semantic log clustering | NEW | S-M | Headline compression win; pure stdlib; directly measurable in `evaluate/bench` |
| **P0** | 4a Parallel index build | NEW | M | Biggest perf win on large repos; keep deterministic sort + proofs |
| **P1** | 2b What-if: broken call sites + test gaps + boundary analysis | ENHANCE | M | Completes the plan's flagship "simulate before edit" story |
| **P1** | 1a AST-aware `budget.Fit` (+ fold coverage to 17 langs) | ENHANCE | S-M | Closes the plan's #1 compression claim where it matters most (budget tool) |
| **P1** | 1c Diff compaction (`kern_diff_files --compact` / `kern udiff`) | NEW | S-M | High agent-loop value; must stay view-only |
| **P1** | 3b `@pure` mutability assertions | NEW | M | Deterministic, fits guard_check path exactly |
| **P1** | 5b Event telemetry wiring (guard→bus→webhook + server bridge) | WIRE | S | 3 small wiring changes deliver the whole multi-agent alert story |
| **P1** | 4c Watch debounce: trailing-unify + dependency wait | ENHANCE | M | Cheap first half, valuable; partial re-index later |
| **P2** | 2a `kern calibrate` CLI wrapper | POLISH | XS | One-liner wrap of existing harness |
| **P2** | 2c Draft-code symbol validation tool (lighter than LSP) | NEW | L | Alternative to LSP masking; reuses verify.go primitives |
| **P2** | 3a Sandbox change manifest (post-run diff artifact in audit chain) | ENHANCE | M | Stdlib-feasible middle ground vs syscall manifest |
| **P2** | 3c Taint-lite + test scaffold generation (LLM-assisted) | NEW | L | Deterministic half first; LLM fills probe bodies |
| **P3** | 4b SQLite tiering / ring buffering | SKIP | L | No measured problem; full WAL mirror is fine for local use |

## Notes

- **Verification engineering:** every deterministic change above (1b, 4a, 1a, 3b) must preserve byte-identical outputs where freshness/hash proofs depend on them (index hash proofs, pack determinism, evidence bundles). 4a needs an explicit sort + regression test against current serial output.
- **Zero-dependency rule:** 1b, 1c, 4a, 3b are pure stdlib. 2c (lighter variant), 3c, and any LLM involvement must route through `internal/llm` (provider-neutral, local default) — never a hard dependency.
- **Where this fits:** all items slot into the Kern 2.0 phase structure as MVP1/2 refinements (intelligence layer + verification/governance); none require new architecture. 5b items 1-2 are 3-line wiring fixes worth doing immediately.