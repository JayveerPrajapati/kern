# kern — Repository Exploration Report

**Repo**: `github.com/JayveerPrajapati/kern` · **HEAD**: `ba3ddda` (2026-09-03) · **Module**: Go 1.23, MIT
**Scope**: full-project exploration — layout, architecture, workflows, conventions, history, state, findings, ideas.
**Method**: 3 parallel deep-exploration lanes (topology/build, internal architecture, docs/history) + Oracle review gate + targeted verification of every disputed claim.

---

## 1. Executive summary

kern is a **local, deterministic code-intelligence engine for AI agents** — a single static Go binary that indexes a repository into a symbol/call-graph index and exposes it through five surfaces: a ~135-command CLI, an 86-tool MCP server, a web console, three language SDKs, and host-agent wiring (opencode plugin, Claude/Gemini hooks, 12 setup detectors). The design center is **determinism and privacy**: rule-based analysis (AST/graph/hashes/policy), zero network by default, no telemetry, LLM used only where reasoning is required (planning, summarization) — never for deterministic facts.

**Headline numbers (verified at HEAD)**

| Metric | Value |
|---|---|
| Go source | 701 files · 149,209 LOC repo-wide (`git ls-files '*.go'`); 657 files · 137,743 LOC in `internal/` · **86 packages** (79 flat + 7 under `twin/`) |
| Binaries | 3: `kern` (CLI), `kern-mcp` (MCP server), `kern-server` (web console) |
| CLI surface | ~135 commands/aliases (map router, `cmd/kern/dispatch.go:29-146`) |
| MCP surface | 86 tools registered (`internal/mcp/server.go`), 11 advertised by default, phase-aware routing |
| Index | go/ast (Go) + regex heuristics (18 base languages), opt-in tree-sitter (13 grammars) + SQLite/FTS5 behind build tags |
| Dependency profile | default build = stdlib-only; optional tags `treesitter` (CGO) and `sqlite` (pure-Go) |
| Git history | 100 commits, all since 2026-08-04 (squashed/linear); 16 tags, latest **v0.9.0** |
| Code health | TODO=8 (case-insensitive), FIXME=0, HACK=0, panic()×2; no dead/empty packages; `go test -short ./...` = 88/88 packages PASS |
| Framework catalog | 74 frameworks detected (`internal/fw/`) |

kern's story is a compressed one: the repo was created 2026-08-04 with a synthetic initial commit, then built through phase-gated "Kern 2.0" work (22 phase reports, all PASS) into the current control-plane shape — digital twin graph, engineering memory, governance firewall, multi-agent loop, verification engine, and production-feedback adapters — ending in the recent G-1…G-11 polish wave (relay, audit chain, sandbox network policy, taint scaffolds, LSP, MinHash clustering, and a full duplication-debt cleanup).

---

## 2. Repository map & build surface

### 2.1 Top-level layout

| Entry | What it is |
|---|---|
| `cmd/kern/` | CLI (37 .go files, 24 production) — main.go (653 l), dispatch.go (664 l, command map + flag resolution) |
| `cmd/kern-mcp/` | MCP server entry (~65 l): stdio default, `--http ADDR` → Streamable HTTP over /mcp, SIGINT/SIGTERM graceful drain |
| `cmd/kern-server/` | Web console + enterprise mode (~165 l): `-root` (default `.`), `-addr` (default `127.0.0.1:8090`), `-enterprise` (fail-closed, requires `KERN_AUTH_TOKEN`), `-version`; webhooks via `KERN_WEBHOOKS` async |
| `internal/` | 86 packages (79 flat + 7 under `twin/`; 2 testdata fixture dirs excluded), 137,743 LOC — the entire engine (Section 3-8) |
| `docs/` | architecture/ (15 files), adr/ (0001), audit/ (G-tracker), phase-reports/ (22), examples/, authorized-context.md, evidence-bundle-schema.md, roadmap.md |
| `sdk/` | Go (`sdk/go/kernsdk/`, 29 methods, stdlib), Python (`sdk/python/`, urllib), TypeScript (`sdk/typescript/`, fetch) |
| `python/` | `kern-context` pip shim — downloads the prebuilt Go binary from GitHub Releases on first use |
| `evaluate/` | bench (hard-gated token-reduction + retrieval benchmarks) + calibration harness |
| `scripts/` | retarget.sh, publish-tap.sh, brew-release.sh (Homebrew formula generation) |
| `homebrew/` | `kern.rb` placeholder template (`__KERN_VERSION__`/`__KERN_SHA256__` substituted at release) |
| `.github/` | ci.yml, pr-review.yml, release.yml, reusable `actions/kern-review/`, copilot-instructions.md |
| `.opencode/` | `plugins/kern.ts` (1,729 lines) — output compression + session-event capture |
| `action/` | composite GitHub Action wrapping kern review (`action.yml`, 7 KB) |
| `.kern/` | runtime state (gitignored): index.json (3.2 MB), index.sqlite (8.8 MB), audit/ (1109 entries), events.sock, flight/, locks/ |
| `.blueprint/` | pre-commit blueprint gate evidence — contains a stale ERROR run (exit 2, 467 files, WSL path, 2026-08-31) |
| `.cortexkit/` | empty scaffolding shell (only a .gitignore referencing an external package) |

**Stray root artifacts (all gitignored, ~74 MB total):** `kern` (20 MB), `app.test` (12.2 MB), `index.test` (11.4 MB), `mcp.test` (20 MB), `bench` (9.5 MB), plus `__pycache__/` — local build/test outputs, not committed, but taking disk space.

### 2.2 Build, CI, install

- **Makefile**: `build` (3 binaries → bin/), `build-treesitter` (CGO), `test`, `test-race`, `vet`, `lint`, `bench`, `install`, `hooks`, `release`/`dist` (4 unix targets + windows, all `-tags sqlite`), `mcpb` (MCP bundle + SHA256), `clean`. Version stamped via `-ldflags -X main.version=$(VERSION)`.
- **go.mod**: module `github.com/JayveerPrajapati/kern`, go 1.23. Direct requires: **13 tree-sitter grammars** + `go-tree-sitter v0.25.0`, `modernc.org/sqlite v1.30.1`, `golang.org/x/sys`; indirect: uuid, golang-lru/v2, go-humanize, go-isatty, go-pointer, go-strftime, bigfft + modernc runtime. **The "zero deps" claim is true only for the default build** — the module carries these deps, imported exclusively under `-tags treesitter`/`-tags sqlite` (stubs: `treesitter_stub.go`, `sqlite_store_stub.go`).
- **CI** (`.github/workflows/`): ci.yml — vet, gofmt, `go test -short`, `-tags sqlite` tests, `CGO_ENABLED=1 -tags treesitter` tests, builds all 3 binaries, MCP-catalog parity test; pr-review.yml + release.yml — reusable `kern-review` action, matrix release on `v*` tags with SHA256SUMS + Homebrew tap auto-publish.
- **Installers**: install.sh (curl|sh, SHA256SUMS verification, `go install` fallback, auto `kern setup --detect --global`), install.ps1 (Windows mirror), homebrew formula (ad-hoc codesign for macOS Gatekeeper).
- **Versioning**: `internal/version.Version = "dev"` (version.go:10), stamped at build; each cmd main falls back to `kversion.Version`.

---

## 3. Core data path

The engine's spine: **index → graph → context**.

### 3.1 Indexing (`internal/index/`, `internal/ignore/`)

- `index.Index` (engine.go:39) — symbols, calls (incl. alias callers), inheritance, imports, packages, file hashes, communities, per-language precision, freshness identity.
- **Go**: full `go/ast` resolution (goast.go, cross-file bindings). **Foreign**: regex/heuristic extraction for **18 base languages** (foreign.go:15-53: go, python, js, ts, css, html, md, json, yaml, rust, c, cpp, csharp, java, ruby, php, shell, dart) + SFC/astro variants.
- **Opt-in tiers**: `-tags treesitter` — real AST for 13 grammars (treesitter.go, `TreeSitterAvailable`); `-tags sqlite` — SQLite WAL + FTS5 full-text search (`sqlite_store.go`, `FTS5Search`).
- **Precision model**: `PrecisionByLang` (engine.go:771) — resolved/ast/heuristic; `--precision strict` drops heuristic edges (`WhoCallsPrecise`/`WhatDependsOnPrecise`).
- **Incremental + canonical**: `index.LoadOrBuild` (loadorbuild.go:11 — G-11 canonical; CLI/LSP mirror killed), `WithPriorIndex` reuse, `ReusedResults` (engine.go:431), freshness proofs (identity.go:167/187 — SHA-256 content root + git stamps).
- **Ignore semantics**: `.kernignore` overrides `.gitignore`; nested ignores honored; hardcoded skip-list (node_modules, vendor, .kern, .git, …) (ignore.go:90).
- **Caveat (memory #52)**: cross-package call edges reference callees by qualified name ("db.Do") while node IDs are bare ("Do") — graph traversals must use raw edge endpoints (`crossesBoundary` does BFS over raw edges + resolveEdgeID).

### 3.2 Intelligence graph (`internal/intelligence/`, `internal/intel/`, `internal/twin/`)

- `intelligence.Graph` (graph.go:25) promotes the v1 Index into the canonical `domain.Graph` (provenance + version metadata); `FromIndex` (graph.go:49) — deterministic node/edge construction (symbols, calls, inherits, imports, packages→module, files→file).
- Query surface (queries.go): `WhoCalls` :180, `WhatDependsOn` :196, `WhatDoesXDependOn` :211, `WhatAPIsAffected` :227, `WhatServicesAffected` :259, `WhatEventsAffected` :314, `ProductionCriticality` :362, `WhatTestsCover` :438, `WhatIncidentsAffected` :406 — each with a `*Precise` strict variant.
- `twin/` (7 subpackages: api, data, edges, ids, infra, messaging, runtime) **augments the static graph with operational facts** — API routes (Gin/Express/Flask/FastAPI/net-http), SQL tables + SQLAlchemy models, Docker/Terraform/K8s manifests, Kafka/RabbitMQ/Redis topology, and runtime-source bridges — merged via `twin.MergeIntoGraph` (merge.go:175). This is the "digital twin" concept: static graph + runtime facts.
- `intel/` — the analysis toolbox (56 files): architecture clustering (arch.go, Louvain-style communities), blast radius (`BlastRadius` intel.go:267), change review (`Review` changes.go:297), churn/co-change, dead code, safe-delete, hubs/bridges, search, SARIF, probe, path/why/wiki, fingerprint (`ComputeFingerprint` fingerprint.go:56), guard events, coverage. Mostly pure functions over the index — clean composable design.

### 3.3 Context & compression (`internal/context/`, compress/, budget/, optimize/, …)

- `context.Engine` (engine.go, 834 l) — the token-budgeted context-selection engine producing "context packets" (rules.go, normalizer.go, select.go, snapshot.go, metrics.go with env-overridable `KERN_COST_PER_TOKEN`).
- **Compression stack**: `compress` (log/prompt compressors; **MinHash clustering** minhash.go — G-9, banded LSH for large logs), `terse` (prompt-fluff stripper), `budget` (Fit/FitCode/FitLossless), `optimize` (high-level orchestrator; **`Recorder` + `EnsureRecorder`** optimize.go:76/81 — G-11 canonical recorder), `pack` (paste-ready repo bundle), `swap` (summary↔expand), `code` (fold/summarize), `docsearch` (local doc index, optional Ollama semantic embeddings), `tokenize` (cl100k/o200k counters), `stats`, `brief`, `memory` (project brain), `fetch`.
- **Caches**: `cache` (exact content-hash, TTL + gzip archival — G-7) and `semcache` (deterministic Jaccard semantic cache, threshold 0.60 — no LLM involved).

---

## 4. Serving surfaces

### 4.1 CLI (`cmd/kern/`)

~135 commands/aliases in ~13 domain families: optimization (optimize/preview/compact/project/pack/log/tokens/budget/terse/swap/semcache/stats/diff/export), index/search (index/watch/ast/search/repos/fts/probe/trace/twin/docs/doc_fetch/doc_search), graph (graph/inherits/context/why/wiki/near/walk/path/explore/dead/larges/arch/hubs/bridges/communities/churn/cochange/flows/entries/fw), analyze/plan (analyze/plan/risk/what-if/impact/execute/verify/check-draft/taint/changes/review/heal/udiff/validate), governance (incident/audit/approve/evidence/guard/fingerprint/authorize-context/lock/unlock/status/events/sec/delete/rename/schema), agents (setup/buddy/onboard/hook/commitmsg/commit/remember/memory/recall/learn/modernize/correlate), task/system (task/efficiency/artifacts/run/loop/autonomy/workflow/do/meta/guide/mcp/lsp/version).

### 4.2 MCP server (`internal/mcp/`)

- **86 tools registered, all in server.go** (sole registration site; Oracle-verified unique count). README body and G-6 tracker agree: 86 tools / 94 dispatch cases incl. aliases.
- **Phase-aware routing** (server.go:52-79, 123-134): `Tool.Phase` tags tools explore/plan/edit/verify/meta/cross; `KERN_MCP_FULL=1` exposes the full 86; `KERN_MCP_PHASE` filters the advertised shortlist; `KERN_MCP_SINGLE_TOOL=1`; `kern_meta` NL router always reaches all sub-tools. **Default advertised surface = 11 tools** (defaultTools map, server.go:247): kern_meta, kern_explore, kern_impact, kern_review, kern_search, kern_context, kern_optimize_prompt, kern_plan, kern_verify, kern_run, kern_authorize_context. **KERN_MCP_PHASE is NOT a security boundary** (documented at server.go:79).
- Transports: stdio (stdio.go:26) + Streamable HTTP (http.go:31); protocol 2025-06-18.
- **Security model**: every tool audited via `toolAudit()` (chained SHA-256 audit entry per dispatch — G-2); `checkRootArg`/`checkWithinWorkspace` root confinement (server.go:1346/1363); `WithPreToolHook` (server.go:1278) governance gate; **MCP confinement default-on** (fail-closed to cwd; `KERN_MCP_PERMISSIVE=1` opt-out — CHANGELOG breaking-change note); exec tools gated by `governance.CheckExec` (KERN_TOOLS/KERN_ALLOW_EXEC).
- Execution tools (kern_exec/sandbox/execute) fail closed; script output PII-masked (memory #50).
- **Monolith note**: server.go is 2,728 lines — the #2 file in the repo; the dispatch switch remains (G-11 "expensive tier" deferred).

### 4.3 Web console (`internal/web/`, `cmd/kern-server/`)

- Prebuilt engines at startup (web.New — no per-request reindex; memory #55, the #1 historical bottleneck fixed in a0875c8); 5s-TTL cached architecture view.
- Routes: HTML dashboard + `/api/overview|graph|memory|incidents|architecture|governance|performance|approvals/pending`, `/task/{id}` detail, v1 REST (`/v1/analyze|plan|what-if|impact|verify`, memory CRUD, graph queries, incidents) + governance approve/reject.
- **Not read-only**: 17+ mutating POST routes (G-6; misleading "read-only L0" wording already removed from docs). Enterprise mode (internal/enterprise) adds org memory/tasks/search/agents with `KERN_AUTH_TOKEN` fail-closed; per-project isolation + LRU eviction (max 16 projects).

### 4.4 SDKs & agent wiring

- **Go SDK** `sdk/go/kernsdk` (client.go, 278 l, stdlib net/http, ~29 methods mirroring /v1 REST); **Python** (urllib, snake_case); **TypeScript** (fetch, no runtime deps). All target kern-server's REST API.
- **opencode plugin** `.opencode/plugins/kern.ts` (1,729 lines, @opencode-ai/plugin 1.18.25): in-place output compression via temp-file pass (threshold 4000 chars) + session-event capture (file edits, failing commands → project memory). **Four copies must stay in sync** (project, ~/.config/opencode, ~/.opencode, embedded setup asset) — memory #167.
- **setup/**: 12 host detectors (Claude, Cursor, Codex, Gemini, Continue, Windsurf, Zed, Qwen, Qoder, Kiro, opencode, Copilot); AGENTS.md written unconditionally (memory #72); capability-gated hooks (opencode plugin, Claude PostToolUse, Gemini AfterTool).

---

## 5. Safety, governance & autonomy

### 5.1 Safety tooling (`internal/`)

- `pii` — heuristic masking (API keys, tokens, IPs, emails; `Mask` pii.go:119, `MaskFile` :263).
- `sec` — secret/security scanner (Go + Python; 7 python rules: eval/exec/os.system/subprocess/pickle/yaml/sql-format — G-4), taint analysis (taint.go), test-scaffold generation (gentest.go, GenPytestScaffold).
- `verify` — post-edit hallucination check (verify.go:54); `within`(mcp) vs `withinAbs`(verify) **deliberately distinct** (Windows drive-root containment — G-11 documented non-duplication).
- `validate` — build/test/lint command detection + run.
- `verification` — unified verification engine (Engine engine.go:43, `NewEngineWithIndex` :59): build/test/security/architecture/dependency verdicts → `domain.Evidence`/`domain.Claim` (evidence.go). Critical security findings block (memory #51).
- `sandbox` — snapshot/rollback sandbox, 100 MiB cap (KERN_SANDBOX_MAX_SNAPSHOT_BYTES), network-policy record (G-3), SkipDirs must include all large dirs (memory #71).
- `heal` (LLM self-correct in throwaway snapshot), `schema` (deterministic JSON-schema validation), `lock` (workspace locks), `evidence` (tamper-evident bundles: RequireEvidence builder.go:70, Generate bundle.go:128), `consistency` (consistency engine), `execution` (artifacts + worktree).

### 5.2 Governance (`internal/governance/` — flattened)

All in root files (subpackage layout was merged 2026-08-31, commit 8c22944 — memory #288):
- **Firewall** (firewall.go:19) — architectural guardrails with agent/task scoping; approvals via FileStore (store.go:34).
- **Audit** (audit.go:55) — tamper-evident SHA-256 JSONL chain, O(1) appends, one entry per MCP tool call (G-2).
- **Approval workflow** (approval.go:23) — `RequiresApproval(level)` :200, persisted; CLI `kern approve`/`kern audit`.
- **Exec gate** (exec.go:40) — CheckExec + approval/resume.
- **Identity** (identity.go:24) — agent registration, permissions.
- **Risk** (risk.go:14) — RiskAssessor + DefaultPolicies.
- **Authorize-context** (authorize.go:42) — computes the legally-readable symbol/edge set for an agent+task with an auditable proof (fingerprint + decision) — the P0.1 primitive behind `kern_authorize_context`.
- **Constitution** (constitution.go:28) — plan/constitution conformance (ADR-0006), SuggestRules.
- **Gateway** (gateway.go:15) — tool-level gate.
- `internal/architecture/` — `boundaries.json` layer rules enforced on files/PRs/projects (validate.go:22/36/46).

### 5.3 Autonomy & agents (`internal/loop/`, agent/, agents/, coder/, planner/, flight/)

- **Loop** (loop/): 9 stages — intent → remember → plan → code → verify → protect → deploy → observe → learn; autonomy **L0–L5** (autonomy.go:14) with per-level stage gates (plan/learn ≥L1, code ≥L2, protect/deploy ≥L4; remember/verify/observe/intent always); **trigger-based escalation** (autonomy_triggers.go: ScopeExpanded, ConfidenceDropped, UnexpectedTool, UnexpectedFile, PolicyChanged, VerificationRegressed); `RecommendedLevel` (autonomy_score.go:145).
- **agents/selection**: TaskKind classification (Code/Documentation/Incident/Modernization/Default) → pipeline variants; `SelectWorkflow` inserts a human-approval step before first execution for every kind (Invariant #2 — governance preserved).
- **agent/**: runtime (Provider, HandoffManager, sessions, tasks, workflow, snapshot, registry). **coder/** + **planner/**: LLM agents (provider-neutral). **flight/**: per-action flight recorder.
- **LLM** (internal/llm): Provider interface (provider.go:47) + factory (NewProvider :88) — ollama (default) / openai / anthropic / google (+6 aliases: openrouter, groq, litellm, vllm, azure → OpenAI adapter); `MaskRequired` privacy gate (:106); embedder adapter for docsearch/intel.

---

## 6. Ops & runtime

- **runtime/** — ops-signal abstraction: `Source` interface (events.go:74), static parsers (Otel/Prometheus/Kubernetes) + **live adapters** (live.go: Prometheus :127, Otel :162, K8s :205 — env-driven endpoints, poll on interval), correlation (`Correlator` correlate.go:29, `SharedCorrelator` shared.go:21), `FormatEvent` canonical renderer (G-11).
- **incident/** — Engine (engine.go:37) tying runtime.Source + memory + firewall; regression injection (:404), store.
- **deployment/** — Deployer interface (deployment.go:29): Noop (default, simulated) + ShellDeployer (:81, runs `KERN_DEPLOY_COMMAND`, **fail-closed unless `KERN_ALLOW_DEPLOY=1`**, 5m timeout, never touches the working tree); Deploy() consults the governance firewall first (approval gate for real deploys — memory #81).
- **cicd/ + ci/** — pipeline model with governance gate (stops at PR creation, never deploys) + GitHub Actions adapter. **prprovider/** — real GitHub PR provider (KERN_GITHUB_TOKEN), noop fallback.
- **storage/** — `LocalStore` (per-key JSON) + `LogStore` (logstore.go:51, append-only chain.jsonl, O(1) Put, TailReader fast path — G-2 follow-up; de-flaked ba3ddda).
- **eventbus/** — typed pub/sub with persistence + idempotency; **relay/** — live socket relay (events.sock, dual-leg durable+live — G-1); **webhook/** — async delivery; **processgroup/** — process supervision; **ownership/** — CODEOWNERS parsing.

---

## 7. State of play

### 7.1 G-tracker (docs/audit/next-plan-gaps.md) — all closed or declined

| ID | Item | Status |
|---|---|---|
| G-1 | Live guard-event streaming (relay dual-leg) | VERIFIED |
| G-2 | Audit chain coverage (per-tool-call chained entries) | VERIFIED (+ LogStore follow-up CLOSED) |
| G-3 | Sandbox network policy manifest | VERIFIED |
| G-4 | Taint scaffold scope (Python + diff/range) | VERIFIED |
| G-5 | Index directory sharding | CLOSED (already sufficient — measured 1.43x on file-level pool) |
| G-6 | Stale docs/claims (84→86 etc.) | VERIFIED — **fix incomplete**: README banner l.30/l.338/l.632 + AGENTS.md:11 still say "84" |
| G-7 | SQLite/cache TTL + dormant archival | VERIFIED |
| G-8 | LSP server (`kern lsp`, stdio JSON-RPC) | VERIFIED |
| G-9 | MinHash log clustering | VERIFIED |
| G-10 | SentencePiece tokenizer | **REMOVED from tracking (declined)** — KERN_TOKENIZER/KERN_MODEL covers the space |
| G-11 | Blueprint duplication debt | DONE (2026-09-02) — all 9+2 confirmed blocks cleared; advisory polish complete |

Tracker header verification was at HEAD `2443259` — one commit behind current tip (ba3ddda). **Hook protocol quirk (memory #305)**: after a dedup commit, the blueprint fingerprint cache goes stale — verify with a no-op `git commit --amend --no-edit`.

### 7.2 G-11 dedup wave (the current commit tail)

Extinction verified: `func itoa` (0 hits in non-test code), `dirMatch` mirror (intel.DirMatch canonical), recorder wiring (optimize.EnsureRecorder canonical), `LoadOrBuild` CLI/LSP mirror killed (loadorbuild.go:11), `fmtRuntimeEventStr` mirror → runtime.FormatEvent. Deliberately kept non-dups: within/withinAbs, pct↔bench.pctf, truncateMCP↔yaml.nextIndent. Net −136 lines on itoa family alone.

### 7.3 Git history & release

100 commits, 2026-08-04 → 2026-09-03, squashed linear single mainline; dominant types: fix: 27, feat: 14, plus subsystem-scoped feat/refactor/perf. Branches: main (current) + feat/kern-2.0-upgrade (local + origin/develop also exist). 16 tags, latest **v0.9.0** — but **CHANGELOG.md documents only [Unreleased] + [0.8.0]** (stale: missing G-7/8/9 features, G-11 wave, storage de-flake, v0.9.0 content).

### 7.4 Test health

- `go test -short ./...` = 88/88 packages PASS (CI enforces -short; long E2E behind testing.Short()).
- evaluate/bench has **hard gates** (`TestGatesAreMet`, evaluate/bench/main.go:839-842): minimums of **25/40/5/75** (prompt/log/terse/budget-fit %); current measured results are 33.3%/60.8%/7.2%/81.8% (README.md:192-195); doc retrieval recall 3/3 @5.
- 22 phase reports all PASS; acceptance matrix test 10/10 (phase-final.md).
- 2,945-line task.go and 2,728-line server.go are the two remaining monoliths — the known G-11 "expensive tier" successors.

---

## 8. Documentation & consistency findings (ranked)

1. **MCP tool count drift (84 vs 86)** — README banner l.30/l.338/l.632 and AGENTS.md:11 say "84"; README body l.467/l.472 and the code say **86**. G-6 claimed the fix; it is incomplete. Agent-facing docs (AGENTS.md) carry the stale number — this is what every future AI session reads first. *(FACT — verified)*
2. **CHANGELOG is 20+ commits behind** — no G-7/8/9, no G-11 wave, no v0.9.0 entry; only [Unreleased]+[0.8.0]. *(FACT)*
3. **"+12 more" badge vs "8 more agents" body** — README l.26 vs l.475; G-6 fixed the body but not the badge. *(FACT)*
4. **G-tracker verification stamp lags HEAD by one commit** (2443259 vs ba3ddda). *(FACT)*
5. **CLAUDE.md/GEMINI.md are stale legacy copies** — 3,334 bytes each vs AGENTS.md 13,517; not mirrors (they predate the expanded AGENTS.md). *(FACT)*
6. **"Zero deps" nuance** — true for the default build, but the module requires 13 tree-sitter grammars + modernc sqlite + x/sys (go.sum 8.5 KB); wording could be "default build is stdlib-only". *(FACT)*
7. **Stray artifacts** — ~74 MB of gitignored test binaries at root (app.test, index.test, mcp.test, bench, kern); .blueprint/ contains a stale ERROR run result; .cortexkit/ is an empty shell. *(FACT)*
8. **Hook protocol friction** — blueprint fingerprint cache goes stale after dedup, needing a manual amend ritual (memory #305). *(FACT)*
9. **External doc dependency** — the G-tracker and two architecture docs reference `support_docs/kern_next_plan/*` outside the repo; docs/audit/next-plan-gaps.md's source of truth is not in-repo. *(FACT)*

---

## 9. Findings & risks

**Strengths**
- Determinedly deterministic: rule-based pipeline, no LLM in the data path; semantic cache is Jaccard, not embeddings; freshness proofs are content-addressed. This is the product's identity and it is consistent.
- Clean engineering hygiene: FIXME=0, HACK=0, 2 panics, 8 TODOs; zero dead packages; CI enforces vet/gofmt/both-tag builds; hard compression gates.
- Governance is real, not decorative: per-tool audit chain, fail-closed exec + deploy, approval workflow, agent/task-scoped authorization with proof. The "control plane" claims are backed by code.
- Single-facade architecture (app.Platform/TaskService under all interfaces) — the right call, repeatedly defended (memory #81 notes a fixer attempt to bypass it was corrected).
- G-11 proved the blueprint gate works: 11 duplicated families found and cleared with a measurable metric.

**Risks / weaknesses**
- **Two monoliths remain**: app/task.go (2,945 l) and mcp/server.go (2,728 l, all 86 registrations + dispatch switch). Both are documented successors for the G-11 "expensive tier". They are the main maintainability debt.
- **Docs drift has a systemic cause**: numbers (tool counts, agent counts) are duplicated by hand across ≥5 files (README ×5 spots, AGENTS.md, CHANGELOG, tracker, code comments). No single source of truth; the G-6 "fix" already partially regressed.
- **Enterprise surface complexity**: web console has 17+ mutating POST routes, yet local mode has no auth — posture differences between local/enterprise are documented but easy to misread; worth an explicit auth-posture matrix.
- **Release process gaps**: CHANGELOG generation is manual (release.yml stamps binaries/SHA but not changelog); README/AGENTS "84" drift suggests doc checks aren't in CI (the MCP-catalog parity test covers plugin↔catalog, not docs).
- **100-commit squashed history** makes archaeology hard (no intermediate history, no PRs visible in log); combined with external planning docs, provenance for architectural decisions lives outside the repo.
- **Index precision caveat** (memory #52) is a footgun for any new graph consumer; the qualified-name edge quirk is documented only in memory, not in code docs.
- **Skipped/declined scope is real**: SentencePiece (G-10), SQLite virtualization, index directory sharding — all consciously declined with reasons; don't re-propose without new evidence.

---

## 10. Ideas & opportunities

Each labeled **FACT / INFERENCE / RECOMMENDATION** with traceability.

1. **Single-source the MCP tool count** *(FACT → RECOMMENDATION)*. 86 registrations in server.go vs "84" in README l.30/338/632 + AGENTS.md:11. Derive the number at doc/release time from the registration table, or add a test asserting README+AGENTS.md == registered count (the plugin-parity test already exists — extend the pattern to docs). Highest-leverage doc fix; it's what every AI session reads first.

2. **Backfill CHANGELOG and automate it** *(FACT → RECOMMENDATION)*. Add a changelog step to release.yml (which already stamps SHA256SUMS + kern.rb): either generate from the v*..v* commit range or block release on a CHANGELOG diff check. One-time backfill for G-7/8/9/11 + v0.9.0.

3. **Continue G-11's "expensive tier"** *(FACT → RECOMMENDATION)*. Decompose mcp/server.go: extract per-domain registration blocks (one domain per commit, blueprint block-count as the metric — the same protocol that cleared 11 families). app/task.go lifecycle decomposition is the second candidate. This is the known successor work — the repo's own tracker says so.

4. **Dispatch↔registration parity test** *(INFERENCE → RECOMMENDATION)*. 86 registered tools vs ~94 dispatch cases (incl. aliases) — the gap suggests dead dispatch arms or multi-case aliases. A test asserting every dispatched name is registered and vice versa would close the ambiguity (server_filter_test.go covers advertisement, not dispatch parity). Cheap, deterministic, high-value.

5. **Make the blueprint hook self-heal** *(FACT → RECOMMENDATION)*. G-11's hook-protocol quirk (stale fingerprint cache after dedup → manual `git commit --amend --no-edit` ritual, memory #305) is papering over a cache-invalidation bug. Detect staleness and auto-refresh in the hook instead of failing/blocking.

6. **Clean up the root + stale evidence** *(FACT → RECOMMENDATION)*. Remove ~74 MB of stray test binaries (or add a `clean-artifacts` Makefile target); refresh/delete .blueprint/ stale ERROR results; either fill .cortexkit/ or drop it.

7. **Document the edge-endpoint quirk** *(FACT → RECOMMENDATION)*. The qualified-name call-edge quirk (memory #52) is a footgun for graph consumers; a code comment in intelligence/queries.go or an architecture doc note would prevent a future bug class.

8. **Explicit auth-posture matrix for the web surface** *(FACT → RECOMMENDATION)*. Local mode (no auth, 127.0.0.1 default) vs enterprise (fail-closed KERN_AUTH_TOKEN) vs MCP (fail-closed cwd + KERN_MCP_PERMISSIVE opt-out) — one table in README or docs/architecture would make the security story legible at a glance.

9. **Regenerate CLAUDE.md/GEMINI.md from AGENTS.md** *(FACT → RECOMMENDATION)*. They're 3.3 KB stale copies of a 13.5 KB file; `kern setup` should refresh them (or drop the duplication — AGENTS.md is the universal instruction file per memory #72).

10. **Bring the planning docs in-repo** *(FACT → RECOMMENDATION)*. The G-tracker's source of truth (`support_docs/kern_next_plan/*`) is outside the repo; vendoring the two planning docs into docs/ would make the audit trail self-contained.

**Process meta-finding**: the exploration surfaced one fabricated file-state claim (kern-gate.yml "empty" — actually 1,931 bytes with real content) that was caught by the Oracle spot-check. Lesson: every file-level assertion (sizes, emptiness, existence) in a report must be verified against the git object store or filesystem before landing.

---

*Report generated 2026-09-03 · deepwork session · Phase 1 = 3 parallel explorer lanes + Oracle GATE-1 review (14/15 spot-checks PASS, corrections applied) · Phase 2 = this synthesis + Oracle GATE-2 review. Evidence references cited as file:line throughout; all claims verified at HEAD ba3ddda.*