# kern — Fresh-Eyes Repository Exploration Report

**Date:** 2026-09-28 · **Method:** read-only exploration of the live working
tree at `/Users/jayveer.prajapati/ai_workspace/kern_opensource/kern`.

> **Snapshot note:** this report describes the repo *before* the 2026-09-28
> tree-sitter default-build flip (recommendation A+B). Post-flip, the default
> build includes tree-sitter (CGO required); `-tags notreesitter` opts out to
> the regex path. References to tree-sitter as "opt-in via `-tags treesitter`"
> and to `build-treesitter` reflect the pre-flip state.
**Deliberate constraints honored:**
- **No memory:** every claim below comes from files on disk, not from project
  memory, past-session recall, or assumed prior knowledge.
- **No testcases:** no `*_test.go` file was used as evidence; no test-derived
  fact (test names, assertions, test output) appears in this report.
- Facts are cited by `file:line` where the exploring lane could pin them.
  Anything not verifiable from non-test source is marked **UNVERIFIED**.

---

## 1. What this repo is

`kern` (module `github.com/JayveerPrajapati/kern`, `go 1.25.13`) is a
**local-first code-intelligence and context engine**: it builds a symbol/call
graph of a project, answers intelligence queries over it, exposes the result
through a CLI, an MCP server, and a web console, and layers governance
(firewall, approvals, audit, evidence) plus an agentic-change pipeline
(blueprint/loop) on top. It is designed to be **local-first by default**:
web-doc fetching is isolated in one package (`internal/fetch`), the LLM layer
defaults to a local provider, and other network-touching surfaces (webhooks,
PR providers, live runtime pollers) are opt-in adapters.

Three binaries in `cmd/`:
- `cmd/kern` — the CLI (216 table entries: 200 non-alias commands + 16 hidden
  `alias: true` snake_case mirrors; 25 categories)
- `cmd/kern-mcp` — the MCP server (stdio default, optional Streamable HTTP)
- `cmd/kern-server` — the web console + enterprise server

109 top-level directories under `internal/` (this count **includes**
`internal/mcp`), plus 52 leaf packages under `internal/mcp/` (~161 Go
packages in total). Rough non-test scale from measured lanes: `internal/index`
~35.1k LOC / 35 files, `internal/intel` ~30.4k LOC / 47 files,
`internal/mcp` ~19.8k LOC / 68 files + 52 leaf packages, `internal/setup`
~3.2k LOC.

---

## 2. Build, packaging, docs, CI

### go.mod & dependencies
Module `github.com/JayveerPrajapati/kern`, Go `1.25.13`. Third-party surface
is small and focused: `modernc.org/sqlite` (pure-Go SQLite, opt-out via
`-tags nosqlite`), the tree-sitter family (opt-in via `-tags treesitter`,
`internal/index/treesitter.go:1`, 14 language grammars), `gopkg.in/yaml.v3`,
`golang.org/x/sys`, plus small indirects (`go-humanize`, `google/uuid`,
`golang-lru/v2`, `mattn/go-isatty`). Everything else is stdlib.

### Build & install
- `Makefile`: `build` (version via ldflags), `build-treesitter` (CGO),
  `test`/`test-short`/`cover`/`test-race`/`vet`/`lint`, `install` →
  `~/.local/bin` **with macOS `codesign` re-sign + `xattr -dr
  com.apple.provenance`** (Gatekeeper SIGKILL workaround), `release`/`dist`
  (cross-compile tarballs, SHA256SUMS), `mcpb`, `clean`.
- `install.sh`: `install/upgrade/uninstall/status`, default prefix
  `~/.local/bin` (`KERN_INSTALL_DIR`), sha256 verify, PATH via shell rc
  (`KERN_NO_PATH` opt-out), then auto-wires agents via
  `kern setup --detect --global` (~lines 330–343). `install.ps1` mirrors on
  Windows.
- Version: shared `internal/version.Version` injected by both binaries'
  `init()`; `server.json` reports `0.9.9.1`.

### Docs
`docs/`: `adr/` (0001, 0006, 0009, 0010, 0011), `architecture/ledger-details.md`,
`architecture.md`, `authorized-context.md`, `benchmarks/`, `cli-reference.md`,
`configuration.md`, `duplication-benchmark.md`, `examples/` (kern-gate.yml),
`gates.md`, `index.md`, `mcp/` (protocol, tool-contracts, tool-schemas.json,
versioning), `mcp-client.md`, `privacy.md`, `recipes.md`, `security/`
(signed-releases, threat-model), `skills.md`, `tool-catalog.md`,
`doc-budgets.json`. Root: `README.md`, `CHANGELOG.md`, `CONTRIBUTING.md`,
`SECURITY.md`, `ARCHITECTURE.md`, `CLAUDE.md`, `AGENTS.md`. **No
`roadmap.md` and no `docs/notes/` tree exist on disk.**

### CI
`.github/workflows/`: `blueprint-nightly.yml` (full gate suite), `ci.yml`
(test/lint-and-vuln/e2e), `codeql.yml` (SAST), `kern-gate.yml` (kern self-gate),
`release.yml` (version-tag release), `test.yml` (reusable workflow_call).
`codecov.yml`: project floor 55% (threshold 2%).

### Other top-level entries
`action/`+`action.yml` (GitHub Action running kern locally, no egress),
`evaluate/` (bench + calibration), `examples/ci/`, `homebrew/kern.rb`
(template), `schema/findings-schema.json`, `sdk/` (contract + go/python/ts),
`server.json` (MCP registry descriptor), `python/` (python/kern package),
`scripts/`, `bin/` (build output), root `kern` (36MB Mach-O arm64 build
artifact), `opencode.json` (registers kern-mcp, `KERN_ALLOW_EXEC=1`),
`package.json` (npm stub).

### opencode plugin
`.opencode/plugins/kern.ts` (~148KB): resolves the kern binary
(`KERN_BIN` → `bin/kern` → PATH), passes large text via temp files, exposes
kern tool calls (functions ~lines 533–663). Embedded copy at
`internal/setup/assets/plugin/kern.ts`.

---

## 3. CLI surface (cmd/kern)

### Entry & dispatch
`main()` at `main.go:209` → `resolveCommandAndFlags()` (`dispatch.go:13`),
injects the live MCP catalog for drift checks (`main.go:215`), loads a metrics
snapshot (`main.go:226`), runs `dispatchCommand`, recovers the `exitError`
sentinel panic into the exit code (`main.go:231-246`), saves metrics, one
`os.Exit` (`main.go:255`).

Dispatch is **table-driven**: `commandTable = map[string]commandEntry{...}`
(`dispatch_table.go:60`); each entry binds `run func(cmd, rest []string) int`
plus help/usage/category/alias (`dispatch_table.go:9-41`). Unknown commands
exit 2 with a Levenshtein nearest-match suggestion (`dispatch.go:237-333`).

### Command inventory
216 table entries, of which **16 are `alias: true` snake_case MCP-mirror
spellings** that dispatch but are hidden from help listings
(`dispatch_table.go:33-40`, `main.go:38`); the remaining ~200 are non-alias
commands. 25 categories drive `kern --all` / `kern help <category>`
(`cliCategoryOrder`, `main.go:87-90`). Families observed: governance (13),
security (5), memory (3), evidence (5), autonomy (18), review (4), exec (5),
meta (8), compression (15), graph (28), search (8), mcp-mirror (32),
wiring (10), servers (10), context (9), locks (8).

### Flag parsing
**One shared parser**: `type flags struct{...}` (`flags.go:21`) — the single
flag surface; legacy per-command `flag.NewFlagSet` parsers were migrated into
`parseFlags` (`flags.go:285`). `parseFlagsOrDie` (`helpers.go:818`) fatals on
bad flags. Flags include `--json`, `--root`, `--model`, `--detect`,
`--global-rules`, `--agents-md`, `--budget`, `--ci`, `--mermaid`.

### Exit-code conventions
`exitError` sentinel (`helpers.go:666`): `fatal()` → 1 (runtime error),
`fatalUsage()` → 2 (usage), `fatalPolicy()` → 3 (decided-state/policy
outcome) (`helpers.go:675,688,697`). Unknown command → 2. Security findings
at error severity and `check`/`ci` gates exit 3; `verify` exits 1 only on a
FAIL verdict; `search` no-match exits 1 while empty `--json` stays 0.

### Env/config surface
`KERN_INSTALL_SCRIPT_URL`, `KERN_FORCE`/`KERN_PIN`/`KERN_VERSION`/
`KERN_CHANNEL` (installer), `KERN_ADDR` + `KERN_AUTH_TOKEN` (serve,
fail-closed), `KERN_MCP_ROOTS`/`KERN_ROOTS` (MCP confinement),
`KERN_MODEL` (task). Config files delegate to `internal/config` (env >
`.kern/config.json` > defaults, `internal/config/config.go:2-7`).

### Wiring & notable mechanisms
Imports **87 distinct `internal/*` packages** (index, intel, mcp, bpcli,
agent/agents/loop, governance/evidence/sec, setup/host/skills,
config/cache/metrics/version/storage, sandbox/heal/validate/gates/
verification). `kern update` delegates to `install.sh` (`cmd_update.go`),
forwarding policy env; version stamped via `kversion.Adopt` (`main.go:27,29`).
`mcpCLIAlias` (`dispatch.go:71`) guarantees every MCP tool is CLI-reachable.

---

## 4. Core engines: internal/index + internal/intel

### Index build & storage
Walks a project root, per-file language detection, symbol+edge extraction →
one `Index` struct (`engine.go:30`), schema `indexVersion = 13`
(`engine.go:27`). Go parsed with `go/parser`+`go/ast` (`goast.go:83-87`);
other languages via tree-sitter (`treesitter.go`, **opt-in** via
`-tags treesitter`, 14-language grammar map `:34`) or regex heuristics
(`extractForeignRegex`, `foreign_lang.go:284`) in default builds. Precision
tiers recorded: Go/Java = "resolved", others "ast" or "heuristic"
(`engine.go:1080`).

Storage is **SQLite-primary**: `SaveSQLite` → `<root>/.kern/index.sqlite`
(`engine.go:196-206`, `sqlite_store.go:971`, WAL mode, tables symbols/calls/
callers/inherits/packages/file_imports/communities/meta/FTS5, schema at
`:152`), JSON fallback `<root>/.kern/index.json` (`engine.go:179,233`), gob
binary snapshot fast path (`engine.go:247-260`). Load order: snapshot →
SQLite → JSON (`engine.go:345`). SQLite is the **default** build
(`//go:build !nosqlite` on `sqlite_store.go:1`; `sqlite_store_stub.go` is the
`nosqlite` opt-out).

### Graph model
`Index` holds `Symbols`, `Calls` (owner→`CallEdge{Target, Confidence,
Synth}`), `Callers`, `Inherits`/`InheritedBy`, `Pkgs` (`engine.go:30-160`;
`confidence.go:46,59,28-36` HIGH/MEDIUM/LOW). Canonical query graph is
`domain.Graph{Project, Nodes, Edges}` (`domain.go:62`), node kinds
symbol/file/module/api; promoted via `intel.FromIndex` (`graph.go:81`) with
edge kinds `contains`/`defines`, `calls`, `inherits`/`extends`/`implements`/
`embeds`, `imports` (`graph.go:84,172-230`). Symbol node IDs are
package-scoped (`graph.go:105-118`).

### Lifecycle & exclusions
`LoadOrBuild` → `Update` (incremental) if stale else `Build`
(`loadorbuild.go:20-44`). Staleness via content-addressed identity (git tree
+ content hash) with `FreshnessProof` (`staleness.go`); `WithPriorIndex` for
incremental reuse (`engine.go:629,696`). Parallel build is
byte-for-byte reproducible vs serial (`engine.go:920`). `ignoreDirs`
(`engine.go:570`): VCS dirs, node_modules, vendor, dist/build/out/target,
__pycache__, .venv, `.kern`, `.blueprint`, agent config dirs, graphify-out;
plus `.gitignore`/`.kernignore` via `walkIndexable` (`engine.go:772`).

### Intel query families
Verified to exist with entry points: `arch` (AnalyzeArchitecture), `hubs`/
`bridges`, `communities` (label propagation), `dead` (DeadCode), `larges`,
`testgaps`/coverage (WhatTestsCover + gap lists), `path` (ShortestPath),
`explore`, `near`, `probe`, `trace`, `why`, `changes`/`churn`/`cochange`,
`guard` (CheckBoundaries, boundary-policy DSL), `purity` (CheckPurity),
`risk` (AssessEditRisk), `delete` (DeleteCheck), `cycles` (ImportCycles),
`flows`, `explain`, `surprising` (SurprisingConnections), `semantic_diff`,
`search` (SemanticSearch/RankedSearch), `repos` (cross-repo SearchRepos),
`fingerprint`. Graph queries in `queries.go`: WhoCalls, WhatDependsOn,
WhatDoesXDependOn, WhatAPIsAffected, ProductionCriticality, WhatTestsCover.

### Notable quirks (fresh observation)
Cross-package call edges reference **qualified** callees (`"index.Load"`);
`resolveImportQualified` re-links them to the imported package's same-named
symbol using `nodePkg`/`pkgImports` (`graph.go:70-83`, `queries.go:242`) —
simple names are ambiguous. Constructor-inferred and dispatch edges are
synthesized post-merge (`rewriteConstructorCallees` engine.go:1942,
`addDispatchEdges` engine.go:1390, tagged `Synth`).

---

## 5. MCP server layer (internal/mcp + mcpclient)

### Architecture
`internal/mcp` root (~19.8k non-test LOC, 68 non-test `.go` files):
`server.go` (1,581 lines) holds `Server` (`:136`), `Tool` (`:36`), phase/risk
constants (`:46-57`), `NewServer`/`NewServerForRoot`/`newServerCore` (single
constructor, `:444,465,388`), `Serve` (`:805`), `safeDispatch` (`:897`),
`dispatch` (`:949`), `runTool` (`:1100`), session mgmt (`sessionFor`/`evict
IdleLocked` `:1234/:1258`), inflight tracking, progress (`slowTools` `:574`),
reactive file watcher (`:701`). Handler logic split across **48
`handlers_*.go` files** by family (agents, coordination, compose, context,
doc, evidence, exec, governance, graph, highlevel, memory, merge, meta,
optimize, org, planner, review, security, skill, stream, synthtest,
transform, ...).

### Registration & dispatch
Tools declared in `catalog/tools.go` (`var All = []Tool{...}`, `:12`) —
**139 entries**, mirrored by 139 entries in `dispatchTable`
(`dispatch.go:39-185`). `catalog.Tool` carries Name/Phase/RiskLevel/Category/
SchemaVersion/Cacheable/Slow (`catalog/catalog.go:18`). Dispatch is **table
lookup, not a switch** (`dispatchTool`, `dispatch.go:189`), gated by RBAC
(`rbac.CheckAgentTool`, `:213`). `CallTool` (`:217`) is the trusted-loopback
CLI path; `CallToolGoverned` (`:237`) the governed passthrough. `runTool`
chains precheck (allowlist+root), arg coercion, a D1 tool-response cache,
metrics, audit (`server.go:1100`).

### Transports
stdio: `ServeStdio` (`stdio.go:40` → `transport/stdio.go`, owns signal
handling + 5s drain). HTTP: `ServeHTTP` (`http.go:31`) implements **Streamable
HTTP** (JSON-RPC POST `/mcp`, no SSE, `:41`), protocol versions 2024-11-05 /
2025-03-26 / 2025-06-18 (`:22-28`). `ResolveHTTPAddr` (`:66`) defaults to a
0600 unix socket in a 0700 temp dir, loopback TCP fallback
(`KERN_MCP_TRANSPORT=tcp`); TLS opt-in via `KERN_MCP_TLS_CERT`/`KEY`
(`transport/transport.go:33-58`).

### Security & confinement
- `checkRootArg` (`server_paths.go:115`) rejects path-typed args outside
  workspace roots; `rootedPath` (`server.go:1086`), `withinRoot` (`:1298`),
  `validateRoot` (`:1353`) resolve symlinks and reject `..`/absolute escapes.
- `gate.go` `Gate.Check` (`:111`) applies `KERN_MCP_ROOTS`/`mcp.roots`
  confinement as the default pre-tool hook (`server.go:484`);
  `KERN_MCP_NO_CONFINE=1` opts out (`:416`).
- Exec gating: `governance.CheckExec`/`CheckExecCommand`
  (`internal/governance/exec.go:53,:69`) fail closed via `KERN_ALLOW_EXEC`
  and the `KERN_TOOLS` allowlist, with persisted command-hash-bound
  approvals.
- `KERN_TOOLS` allowlist (`toolpolicy.go:20,:127,:327`); `KERN_MCP_FULL`/
  `KERN_MCP_PHASE`/`KERN_MCP_CATEGORY` only filter advertisement, never
  execution (`:257,:64`).
- Doc names sanitized: `SanitizeDocName` (`doc/doc.go:60`).

### Leaf packages (52 dirs)
agentctl, bridge (external MCP forwarding `kern_mcp_call`), blueprint
(change-firewall tools), catalog, compose, context, contextwatch, coord,
crossrepo, deploy, doc, envelope, evidence, exec, explain, fingerprint,
flight, fragility, gov, governance, graph, health, highlevel, lsp, mcpargs,
memory, merge, meta (NL classifier/router), mutation, optimize, orchestrate,
org, planner, policydsl, preedit, prompt, prose, provenance, rbac, refactor,
repair, retrieve, review, root (`ResolveRoot`), runtime, security, skill,
stream, synthtest, transform, transport, watcher.

### mcpclient
`internal/mcpclient/client.go` (460 lines): `Client` (`:334`) with `Dial`
(`:341`, auto stdio/http/unix), `ListTools` (`:362`), `CallTool` (`:392`),
config load/save (`:422/:439`), `FindServer` (`:452`). Speaks JSON-RPC
(`ProtocolVersion`, `:32`); exercised server-side via `kern_mcp_call`
(`handlers_mcpclient.go`) — kern can call external MCP servers and bridge
their tools.

---

## 6. Governance, verification, app, domain & web layer

### internal/app (~9.7k LOC)
`TaskService` (`NewTaskService(p *Platform, bus *eventbus.Bus)`, task.go:81)
wires identity/audit/PR-provider/persistence (`task.go:129-187`).
`Run(intent)` (`task.go:298`) = `CompileIntent` → `SelectWorkflow` →
`DefaultCapabilities` → `CapabilitiesToTools/Agents`, then risk aggregation,
task creation, unified `PolicyPrecheck`, returning `domain.RunResult`.
`CompileIntent` (`intent.go:26`) classifies raw text; `SelectWorkflow`
(`intent.go:85`) maps to A_UNDERSTAND/B_SAFE_CHANGE/C_PREDICT/D_OPERATE/
E_GOVERN (`domain/intent_type.go:35-39`); `DefaultCapabilities`
(`intent.go:108`) derives capabilities whose `Risk` drives approval. Ten
intent types (`domain/intent_type.go:8-17`). `Platform` (`platform.go:55`)
is the shared facade owning index/graph/memory/firewall/context/verification
engines (`New`/`NewWithIndex`/`NewWithGraph`). Enterprise seam:
`enterprise.ProjectApp` (`enterprise/app.go:30`) wired by
`Server.SetAppFactory` in `cmd/kern/cmd_serve.go:187`.

### internal/governance (~5.5k LOC)
`Firewall.Check(agentID, resource, action)` (`firewall.go:205`) is a
five-stage **fail-closed** pipeline: (1) agent authentication — unknown
denied (`:208-218`); (2) authorization via `agent.Can` (`:220-232`);
(3) risk scoring; (3.5) egress policy, local-only default (`:234-267`);
(4) always-blocked CRITICAL `drop` (`:268-283`); (5) approval gate for
HIGH/CRITICAL (`RequiresApproval`, `:284+`, approval.go:255).
`RiskAssessor` (`risk.go:15`) maps resource+action → level via
`DefaultPolicies` (`risk.go:35-46`); HIGH/CRITICAL set `ApprovalRequired`
(`risk.go:201`). `ApprovalWorkflow` (`approval.go:25`) Request/Approve/
Reject (`:75-199`), persisted under root (`:45`). `AuditLog` (`audit.go:83`)
with atomic sequence counter. `AuthorizeContext` (`authorize.go:43`) computes
legal read context + `AuthorizationProof`. Exec: `CheckExec` (`exec.go:53,69`),
approval-gated commands get HMAC-stamped one-shot consumed approvals
(`exec_approval.go:39-398`). Files: firewall, approval, audit, identity,
authorize, risk, exec, exec_approval, egress, rbac, secrets,
org_policy_store, constitution, gateway.

### internal/verification (~4.6k LOC)
`Engine` (`engine.go:48`) via `NewEngine`/`NewEngineWithIndex`
(`engine.go:62,70`). `Verify(types)` (`:130`) runs build, test, security,
architecture, dependency, e2e, static-analysis, performance (+CI when
adapter set). Verdicts (result.go:17-39): PASS, FAIL, WARN,
PASS_WITH_WARNING, BLOCKED, NOT_RUN, SKIPPED. Security mapping
(`engine.go:844-852`): `sec.SeverityError`→Critical, Warning→High,
Info→Low; critical sets `res.OK=false` (blocks), lower severities surface as
warnings. `VerifySecurity` emits evidence-backed claims (`:835`).

### internal/context (~3.9k LOC)
`Engine` (`engine.go:35`) built with root/graph/memory/firewall.
`AnalyzeChange(symbol)` (`:167`) assembles a `ContextPacket`; `AnalyzeFile`
(`:182`) roots every symbol in the file; `analyzeIntent` keyword-classifies
(`:197`). `assessRisk` (`:507`) maps change roots → governance
resource+action via `resourceForAction` (`:615`) and calls `firewall.Check`
under the `"context-engine"` agent — firewall error ⇒ `Blocked`, pending
approval ⇒ `ApprovalRequired` (`:519-545`). `crossesBoundary` (`rules.go:94`)
walks call edges for policy-boundary crossings; `parseBoundary`
(`rules.go:214`).

### internal/domain (~2.4k LOC)
Flat, interface-agnostic canonical types (`doc.go:1`): `domain.go` (Project,
Symbol, Graph, Node, Edge, Claim, Evidence, Policy, Risk, Approval, Task,
Plan, ImpactReport, RiskLevel ladder at `:272`), `entities.go` (Service, API,
Database, Commit, PullRequest, Artifact, VerificationResult), `runtime.go`
(Severity, Alert, Deployment, Incident, Hypothesis, RootCause),
`context_packet.go`, `intent_type.go` (workflow/intent enums).

### internal/architecture (~1.4k LOC)
`Config`/`Layer`/`Rule` from YAML (`config.go:20,29,38`; `Load` `:64`, 1 MiB
cap). `Engine.Check` (`engine.go:34`) layer-rule checks; `ValidateProject`/
`ValidateProjectWithIndex`/`ValidateDiff` (`validate.go:22,36,46`) → Report.
**ARCHITECTURE.md parity**: `ParseArchDoc` (`parity.go:67`) +
`ParseLedgerDetails` (`parity.go:110`) joined by `CheckArchDocParity`
(`:229`) enforce per-subsystem LOC caps + allowed deps from
`ARCHITECTURE.md` + `docs/architecture/ledger-details.md` against actual
imports → `ArchDrift` (`:200`).

### internal/incident (~1.1k LOC)
`Engine` (`engine.go:38`) via `NewEngine`/`NewEngineWithGraph`
(`:53,68`). Pipeline (doc `engine.go:6`): `IngestAlert` (`:143`) →
`Correlate` (`:203`, via `runtime.SharedCorrelator`) → `RootCause`
(`:286`) → `ApplyAndVerifyFix` (`:346`) → `RequestApproval`/`Approve`
(`:140,462,467`) → `CreateFixPR`/`FixAndPR` (`:499,544`).
`runtimeEvidence` (`:709`) converts correlation output to `domain.Evidence`.

### internal/web (~4.4k LOC)
`web.New` (`web.go:254`) loads/rebuilds index + graph + `app.Platform` via
`NewWithGraph` **once at startup** (`:283`), then a single `TaskService`,
approval workflow, incident store, agent registry (`:295-322`).
`registerRoutes` (`:453`): `/api/*` JSON, `/v1/*` REST (analyze, verify,
loop, tasks, incidents, approvals, events/stream), HTML pages (`/task/`,
`/incidents`, `/architecture`, `/graph`, `/memory`, `/eval`). `ServeHTTP`
(`:525`): opt-in bearer gate `KERN_AUTH_TOKEN` (constant-time, `:44,574`),
per-IP rate limiting (`:551`), CSRF/DNS-rebinding guard (`:569`). Multi-
project via enterprise `SetUserRoleLookup`/`SetPolicies` (`:846,859`).

---

## 7. Agentic & autonomy family

### internal/blueprint (~6.4k LOC)
A **change-governance validation engine**: gates a `ChangeRequest` through a
canonical validation pipeline. Entry: `service.New`/`service.Validate`
(`service/validate.go:174,192`) runs registered `Check`s + `PolicyEvaluator`,
aggregating monotonically ERROR>BLOCK>WARN>PASS>SKIP (`:440`). Model in
`domain/types.go` (ChangeRequest, ValidationResult, Finding/CheckResult,
Status, Severity, Category, Enforcement, ContextProvenance `:199`);
`domain/legs.go` maps check-name prefixes → LegKind. `adapters/kern/client.go`
`KernClient` (`NewKernClient:130`, `GuardCheck:278`, `AuthzVerdict:372`,
`SecScan:547`, `KernContractVersion=36`); `audit/` self-hashed append-only
with flock; `sandbox/` `Run`/`RunInWorktree` (netns per platform,
`worktree_manager.go` GC), and `check.go`'s `Check` (`"tests:build-test"`)
runs `go build`+`go test` in a fresh git worktree after `applyStagedDiff`
(`sandbox.go:249`); `checks/diffgate/` adds GofmtCheck, VulnCheck,
SchemaDriftCheck, ExecUnsafeCheck, ChangelogCheck, CatalogDriftCheck.

### Extracted family
- `bppolicy` (1.1k): `policy.Engine.Evaluate` monotonic enforcement
  (`policy/engine.go:56`), `risk/classify.go`.
- `bpreceipt` (991): `receipt.Receipt` Generate/ComputeSignature/Verify
  (`receipt.go:65,104,119`) — tamper-evident CI receipt; attestation/store/
  graph/metrics/version.
- `gates` (1.0k): `registry.go` — `Gate` + ordered `Registry` of 39 gates
  G0–G39 (**G10 retired**, `registry.go:48`); approval_store, check.
- `scanners` (2.1k): gitleaks (`"secret:gitleaks"`), duplication
  (`"duplication:advisory"`), jscpd (`"duplication:jscpd"`).
- `resilience` (1.2k): `"resilience:scenarios"` check (`scenario.go:12`).
- `bpcli` (6.5k): Blueprint CLI — check/diff-gate/fix/metrics/
  request-approval/reject/verify-receipt/ci/install, standalone or under
  `kern <sub>`; `mcp/` exposes its own MCP server.

### agents / agent
`internal/agents` (`selection.go`): `ClassifyTask` (`:72`), `SelectPipeline`
(`:41`), `PipelineForKind` (`:93`, no approval), `SelectWorkflow` (`:104`)
which **inserts a human approve/RequiresApproval step before execution**;
`pipeline.go` `Run` via `agent.HandoffManager`; `RoutingContext.RankRoles`/
`RouteFor`. `internal/agent`: `provider.go` `Provider` interface;
`workflow.go` `WorkflowEngine` (`Run`/`RunContext`, `DefaultWorkflow` 7-step
request→analyze→plan→approve→code→verify→pr, `ErrApprovalRequired`);
registry/session/task/taskstore.

### coder (~490 LOC)
`Agent.Code(intent, plan, codeContext, wt)` drives an LLM to produce edits,
applies patches (`applyEdits:427`, `validEditPath:465`), self-verifies over
rounds (`WithMaxRounds`, `WithVerifyTypes`) → `Result{Passed, Diff, Rounds,
PromptTokens, TotalTime}` (`coder.go:89`); `ErrNoProvider`,
`ErrBudgetExhausted`.

### loop (~2.0k LOC)
Autonomy levels L0–L5; **nine stages** `intent→remember→plan→code→verify→
protect→deploy→observe→learn` (`autonomy.go:31-40`). `AllowsStage`:
plan/learn≥L1, code≥L2, deploy/protect≥L4, remember/verify/observe always;
`AllowsStageWithProofs` — L5 write/act requires all `L5Proofs` (policy/
verification/rollback/monitoring/audit/confidence; nil fails closed).
`LoopConfig` (`loop.go:41`) wires Root/Level/Proofs/Service/ObserveWindow/
Source/Mem/Incidents/Appr/Deployer/Learning/PatternThreshold/Recorder/Coder/
Context/Planner/Budget/MaxRiskLevel/AssessRisk/PauseTrigger/Firewall/
MaxRepairAttempts/WorktreeManager. `RunContext` (`:289`) iterates stages,
tracks a safety budget, pauses on budget/risk/approval. On firewall BLOCK it
runs `runAutoRepairLoop` (`:573`); deploy requires `KERN_ALLOW_DEPLOY=1`.

### Supporting
- `execution` (658): `worktree.go` `NewWorktree`/`Apply`/`Diff`/`Cleanup`.
- `flight` (447): `Recorder` (`Record`, `List`, WhyDecision/WhatContextUsed/
  WhichToolsCalled/WhatChanged/WhatVerified/WhatOutcome).
- `learning` (698): `Extractor` (`New`/`Patterns`/`Surface`/`Remember`);
  architecture_drift/context_usage/policy.
- `memory` (2.0k): `MemoryStore` (`Add`, `Recall`/`AuthorizedRecall`,
  `Supersede`, `List`/`Get`/`Update`/`Replace`); governance/retention/query/
  ranked/audit.
- `llm` (1.7k): `Provider` (`Generate`/`Embed`/`Capabilities`/`Stream`),
  `NewProvider` by `KERN_LLM_PROVIDER` incl. `auto` chain (`provider.go:94`);
  `NewEmbedder`; chain/localcli + ollama/openai/anthropic/google/mcp.
- `skills` (402), `council` (426: `FromPack`/`Normalize` → Consensus/
  Divergence/Minority/Unsupported/Driver).
- Orchestration has no top-level `internal/orchestrate` package; it lives in
  `cmd/kern/cmd_orchestrate.go` (`kern orchestrate "<intent>"` with
  `--mode fix|review|architecture|incident|explain|envelope|plan|full`,
  `cmd_orchestrate.go:15,44`), `internal/context/orchestrate.go`, and
  `internal/mcp/orchestrate/`.

---

## 8. Storage, config, runtime & infrastructure

### Storage
`internal/storage` (697): `Store` interface (`:31`); `LocalStore` JSON
key/value (`NewLocal(dir)` `:47`); `LogStore` append-only JSON-lines chain
log (`logstore.go`); `OpenSQLite(path)` (`sqlite.go:24`, **default build**
`//go:build !nosqlite`; `sqlite_stub.go` is the opt-out) with
`SQLitePragmas` (`:14`). Data lives under per-root `.kern/`.

### Config
`internal/config` (645): JSON `<root>/.kern/config.json` + per-key env
overrides; every getter resolves **env > file > default** (`resolve` `:123`),
file parsed once per root and cached (`fileCache` `:38`). Typed getters
String/Int/Float64/Bool/Duration/StringMap/Strings (`:145-387`). `Registry`
(`:455`) enumerates configurable keys + env vars (KERN_LLM_PROVIDER,
KERN_MODEL, KERN_EMBED_MODEL, tokenizer/cost, cache TTLs, KERN_POLL_INTERVAL,
KERN_PROMETHEUS_URL, KERN_OTEL_URL, KERN_K8S_*, deploy command/timeout,
KERN_EXEC_RISK, webhooks, enterprise projects). `modelRoleEnvKeys` (`:402`);
`resolveMCPRoots` (`:440`) reads KERN_ROOTS/KERN_MCP_ROOTS. **Secrets and
safety toggles are env-only** (`:9-16`). `kernconfig.go` adds YAML profile
`.kern/kern.yaml` (`Load` `:107`, TruncateRule/ProfileConfig).

### Setup
`internal/setup` (3.2k): `Wire`/`WireWith` (`setup.go:222,238`) writes
`.mcp.json`, opencode config + plugin, `AGENTS.md`; **12-entry adapters
registry** (`:113`) for Continue, Windsurf, Zed, VSCode, Cursor, Gemini,
Antigravity, Qwen, Qoder, Kiro, Copilot — `stdioEntry`/`cmdEntry`
(`:128-156`) emitting `KERN_ALLOW_EXEC=1`. Per-agent hooks in
`setup_*_hooks.go`. `go:embed` assets: plugin `assets/plugin/kern.ts`,
`assets/AGENTS.md`, `assets/global-rules.md`, `assets/hooks/kern-guard.sh`,
per-agent instruction files. `ensureKernConfig` (`:408`) scaffolds
`.kern/skills` + `.kern/profiles.json`.

### Runtime intelligence
`internal/runtime` (2.0k): `Source` (`events.go:74`) = Events/Deployments/
Commits; `Event` (`:28`, metric/log/trace/error), `Commit` (`:63`).
Adapters are pure parsers: `ParseOtel`/`ParsePrometheus`/`ParseKubernetes`
(`adapters.go:54,155,289`); live pollers `liveSource` (`live.go:19`,
`NewLivePrometheusSource` `:128` etc.). `Correlator` (`correlate.go:29,46`)
maps `domain.Alert` → affected service + evidence within a lookback window
(`resolveService` `:83`). Offline path: `Snapshot`/`LoadJSON` (`local.go:13,22`).

### Messaging & integration
`internal/eventbus` (764): stdlib-only in-process pub/sub (`Bus` `:233`),
`New` (`:321`, idempotent), `Subscribe` (`:334`), async `Publish` (`:383`)
with panic-retry/dead-letter, bounded history (10k default,
`KERN_EVENTBUS_MAX_HISTORY` `:300`), JSONL persistence + `Replay`
(`EnablePersistence` `:685`, `.kern/events.jsonl` `:695`), ~90 typed `Kind`s
(`:26-187`). `internal/relay` (428) broadcasts events over a local Unix
socket `.kern/events.sock`; `internal/webhook` (241) POSTs eventbus events
as JSON.

### LSP
`internal/lsp` (658): minimal **LSP server** over stdio (Content-Length JSON-
RPC 2.0; `Serve` `:69`, `readMessage` `:114`), loads index at `initialize`
(`:255`), serves hover/definition/references. v1 limits: line-only precision,
ASCII identifiers, no doc comments. `internal/lspbridge` (996): **LSP client
bridge** to external servers (gopls, pyright, rust-analyzer, clangd) —
`DefaultServerRegistry` (`:34`), `DetectServer` (`:134`), `StartClient`
(`:281`), pooled `Query` (`:696`).

### Locking / concurrency
`internal/lock` (378): advisory workspace locks (flock on Unix, exclusive-
create on Windows), files under `.kern/locks/` (`:56`), auto-release on
process exit, `ErrLocked`, `ContentionHook` (`:30`). `internal/flock` (105):
low-level cross-platform blocking exclusive flock wrapper.

### Secondary infrastructure one-liners
cache (662) — local state cache, gzip archive, `Maintain` eviction ·
version (461) — build-stamped version + release-channel resolver ·
semcache (890) — deterministic local semantic cache (Jaccard word-shingle
near-dup) · processgroup (46) — run command in own process group ·
host (391) — injects context block into host instruction files · hook (489)
— native per-agent hooks for output compression + session-memory capture ·
fsutil (62) · ignore (288) — gitignore-style, `.kernignore` > `.gitignore` ·
script (759) — runs code in isolated runtime, returns stdout only ("Think in
Code") · fw (1.3k) — framework catalog (Spring/Django/Express…) + detection ·
twin (1.7k) — entity-level knowledge graph of endpoints/DBs/tables/topics/
services · integration (0) — **test-only, no non-test Go files** · fetch (245)
— the isolated web-doc fetching package (plain-text extraction, cached on
disk) · precache (204) — background daemon warming code-summary/vector
caches.

---

## 9. Analysis, search, refactor & review tooling

### Project & pack
`internal/project` (1.4k): `Session` facade — project root + lazily-loaded,
auto-refreshed symbol index with **stale-while-revalidate single-flight**
rebuilds (`project.go:160-230`); prefers SQLite load → incremental `Update`
(capped by `index.CatchUpMaxChanges`) → full `Build`; saves off the critical
path, drained by `Close()` (`:249-320`); inotify/kqueue watcher invalidates
cache (`:326`); caches derived intel + telemetry (`:365-413`).
`internal/pack` (731): `Build` (`pack.go:102`) → paste-ready `Bundle`
(root instruction docs + token-counted tree + contents fitted to token
budget, skip-and-continue at file granularity `:240-265`); SHA-256-ordered
for deterministic re-packs (`:352`); `Render` markdown (`:381`) / `JSON`
(`:485`); security scan up to 25 findings (`:262-285`); `BuildGraph`
(`graph.go:45`) packs a call-graph snapshot at 1–5% of file token cost.

### Search & retrieval
`internal/docsearch` (866): local dependency-free semantic doc search —
feature-hashed character n-grams (FNV, deterministic) (`docsearch.go:108`);
optional dense embeddings via local Ollama `Embedder` (`:183`); `Search`
(`:385`) fuses **n-gram cosine + dense cosine + BM25** by Reciprocal Rank
Fusion (`:412-438`, bm25.go:9-18), threshold 0.20. `internal/retrieval`
(660): progressive disclosure — L1 index summary, L2 symbol neighborhood,
L3 verbatim source (`levels.go:84,196,226`); `RetrieveForTask` picks tier
(`:119`); typed handles with 7-day TTL registry (`handle.go:30,72`) + bounded
LRU content cache.

### Refactor / rename / mutation
`internal/rename` (1.1k): Go symbol rename — find definition, collect
callers, classify every identifier occurrence (`rename.go:78,130,144-165`),
`RenameMethod` all-or-nothing with provable-receiver type-proof
(`method.go:17,120-145`), `Apply` splices byte-offset edits with
backup/restore rollback (`:314,399`). `internal/refactor` (231): atomic
multi-file `ExecuteTransaction` — validate paths, copy to sandbox, apply,
run `CompileCommand`, commit to live root **only on successful compile**
else rollback (`transaction.go:30,45,204`); dry-run diff. `internal/mutation`
(412): AST mutation testing for test-suite gaps. `internal/transform` (525):
AST rewrites. `internal/remove` (338): safe source-level deletion. `internal/
swap` (303), `internal/optimize` (639, prompt/log token optimization with
anchor store), `internal/tokenize` (1.1k, BPE cl100k_base/o200k_base +
char/word estimator), `internal/terse` (945, deterministic LLM-output
compression), `internal/compress` (774, noise stripping), `internal/diff`
(879, line-level unified diffs), `internal/strutil` (88), `internal/schema`
(253, JSON-schema formatting boundaries).

### Evidence & review
`internal/evidence` (1.4k): **tamper-evident evidence bundle** for SOC 2 /
ISO 42001 / EU AI Act (`bundle.go:7-11`). `Generate` (`:153`) assembles four
pillars: authorization proof, freshness, lineage, audit-trail snapshot from
`.kern/audit`. `Verify` (`:280`) recomputes the hash, verifies optional
ed25519 signature (`:334,351`) + key fingerprint/trust chain;
`VerifyWithAnchor` (`verify.go:48`); `Digest` SHA-256 (`digest.go:10`).
`internal/reviewpack` (535): deterministic immutable review packs — commit/
dirty hash, selected evidence with reasons, symbols with call paths, changed
code, tests (`reviewpack.go:1-15`, Build `:133`). `internal/lenses` (281):
named review lenses. `internal/profiles` (283): three-layer evidence model.
`internal/ownership` (150): CODEOWNERS parsing. `internal/prprovider` (274):
PR create/comment abstraction (GitHub `github.go:42`, no-op `noop.go:11`).
`internal/orgapprovals` (918): org approval event records/audit DTOs.

### Validation & what-if
`internal/validate` (797): language-appropriate build/test/lint command
detection + safe run — `Detect` (`:31`), `DetectKind` (`:61`), `Run` with
timeout + capped output (`:372`), per-extension syntax `Checks` (`:97`), skips
reported when toolchain missing. `internal/whatif` (1.0k): applies a
hypothetical change (remove_symbol, change_signature, add_dependency,
split_service, rename_symbol, …) to an **in-memory copy** of the graph and
returns a deterministic `Impact` report — affected symbols/files/services/
tests, broken call sites, untested-affected, risk, recommendation,
alternatives, mitigations, confidence, affected DBs, limitations
(`whatif.go:25-56,63-150`); read-only, never mutates the real graph.

---

## 10. Security, sandbox, deployment & ops

### Security & privacy
`internal/sec` (1.7k): local deterministic line-scoped scanner — hardcoded
secrets, dynamic SQL, shell-command injection, weak crypto, insecure
randomness, unsafe deserialization (`sec.go:4-7`); severities
error>warning>info (`:25-30`); byte-wise regex rules → concrete lines
(`:35-51`); source-code rules (sql-injection, command-injection, unsafe-
deserialization, code-eval, unsafe-reflection, insecure-random, weak-crypto
`:115-121`) + config rules (placeholder-bug `$VAR` vs `${VAR}`,
hardcoded-config-cred, disabled-ssl-verify `:960-962`); `Scan(root)` (`:749`);
heavy false-positive suppression (`isExampleDomain`, `isLowEntropySecret`,
etc. `:372-614`); `taint.go` source→sink reachability; `python.go` Python
sinks.
`internal/pii` (503): regex-based masking (`pii.go:6`, `DefaultPatterns`
`:31`), encoded-secret decode (base64/hex/percent/unicode `:360-439`),
`Mask`/`MaskNames`/`MaskCustom`/`MaskAll` (`:197-221`), reversible `Unmask`
(`:337`). Wired into mcp/security, exec, sampling, tool_cache, optimize,
planner, coder.

### Sandbox
`internal/sandbox` (1.7k): snapshot/restore (100 MiB cap `:58`), `Run`/
`RunGuarded` under timeout with env sanitization (credential scrub +
allowlist `:190-257`), path-escape blocking (`Escape` `:909-941`).
Confinement: macOS seatbelt profile + net-isolation prefix
(`network.go:152,230-252`); Linux **Landlock** leaf
(`sandbox/landlock/landlock_linux.go:82-113`, `LandlockAllowPaths`
`landlock_paths.go:158`).

### Self-healing
`internal/heal` (677): LLM-driven self-repair — `Run` iterates fix rounds
(`heal.go:216`), `Apply` writes `### FILE:` replacement blocks (`:134,189`),
failure detection (`failingFiles`/`failLineRe` `:560-567`),
`EvaluateCandidate` (`:621`), playbook-driven recovery (`RunWithPlaybook`
`:459-508`). `internal/repair` (343): automated AST-level fixes for compile
errors/lints. `internal/fragility` (282): correlates git defect/fix history
with AST-symbol fragility.

### Enterprise & deployment
`internal/enterprise` (2.3k): `Server` (`enterprise.go:44`, `New` `:107`);
project register/unregister with profiles (`:295-342`), LRU-capped app cache
(`appForCached:457`, `evictLRU:512`); org governance — policy write/reload/
apply + drift detection (`WriteOrgPolicy:225`, `applyPoliciesLocked:261`,
`PolicyDrift:276`); token auth (`requireAuth:765`, `authTokenEnv:754`); HTTP
admin surface (org dashboard/API/audit/policies/projects/agents/teams,
`ServeHTTP:794`). `internal/deployment` (165): `Deployer` interface
(`deployment.go:30`), `NoopDeployer` (`:59`), `ShellDeployer` (`:82`, timeout
+ capped output), `NewDeployerFromEnv` (`:147`).

### Metrics & ops
`internal/metrics` (752): in-memory `Recorder` — RecordIndexBuild/
GraphQuery/ContextRetrieval/MemoryRecall/ToolCall/LLMLatency/Approval/
SandboxOp/Incident/Error/TokenUsage (`metrics.go:113-346`), sample-capped,
`Snapshot`/`Render`/`Report` (`:400-568`), `SnapshotWithGovernance` (`:628`),
Save/Load (`:655-674`). `internal/synthtest` (1.2k): synthetic Go test
generation by folding/constant-evaluating the target function
(`synthesize.go:564`, `generateTestFunction:820`), confined to target root
(`confinePath:510`). `internal/ci` (241): vendor-agnostic CI/CD adapter.
`internal/stats` (372): token counts/cost. `internal/code` (936): code
folding/summarization/structure. `internal/commitmsg` (1.2k): deterministic
commit messages + changelog. `internal/cockpit` (1.2k): interactive TUI +
triage runners. `internal/doctor` (1.0k): diagnostics for wiring/index/
Ollama. `internal/calibrate` (770), `internal/fit` (255), `internal/budget`
(212), `internal/docbudget` (124), `internal/eval` (321), `internal/
testfixture` (202), `internal/learnclaim` (90), `internal/modernization`
(812), `internal/planner` (131), `internal/prompt` (52), `internal/brief`
(343).

---

## 11. Cross-cutting observations (fresh-eyes)

1. **Local-first by default.** Web-doc fetching is isolated in one package
   (`internal/fetch`); docsearch/retrieval use local hashed embeddings;
   evidence/audit/approvals all local; the GitHub Action explicitly "no
   network egress". Network-touching surfaces exist but are opt-in adapters:
   `internal/webhook` (POSTs events), `internal/llm` providers
   (ollama/openai/anthropic/google), `internal/prprovider` (GitHub API),
   `internal/runtime/live.go` (Prometheus/OTel/K8s pollers),
   `internal/mcpclient` (dials external MCP servers). The LLM layer defaults
   to a local provider (`auto` chain via `KERN_LLM_PROVIDER`).
2. **Three concentric surfaces over one engine.** CLI (216 commands) ↔ MCP
   (139 tools) ↔ web console (/api + /v1 + HTML) all sit on the same
   index/intel/governance core; parity is enforced (`mcpCLIAlias`, catalog
   drift checks, plugin parity).
3. **Governance is the spine.** Firewall (5-stage fail-closed), approvals
   (HMAC-stamped one-shot exec approvals), audit (atomic sequence), evidence
   bundles (ed25519), and the ARCHITECTURE.md LOC/deps ledger form one
   coherent control plane, mirrored in the change pipeline (blueprint
   validation, loop stage gating with L5 proofs).
4. **Confinement is defense-in-depth.** Workspace-root confinement
   (`withinRoot`/`checkRootArg`), MCP roots gate, `KERN_TOOLS` allowlist,
   exec fail-closed, env-only secrets, sandbox (seatbelt/Landlock/netns),
   doc-name sanitization, symlink-escape blocking.
5. **Scale hotspots.** `internal/mcp` is the largest leaf family (68 files +
   52 subpackages); `internal/index`+`internal/intel` are ~65k LOC of engine.
   `internal/blueprint` has been modularized into a family (bppolicy,
   bpreceipt, bpcli, gates, scanners, resilience). `internal/integration`
   contains no non-test Go files.
6. **Determinism is a design value.** Byte-identical parallel index builds,
   SHA-256-ordered packs, deterministic review packs/evidence digests,
   deterministic commit messages, monotonic status aggregation.
7. **Freshness & concurrency are engineered.** Stale-while-revalidate index
   sessions, single-flight rebuilds, content-addressed staleness proofs,
   WAL SQLite, file-watcher invalidation, advisory locks, eventbus
   idempotency + dead-letter.

---

*End of report. Compiled from live-tree exploration only; no project memory
and no testcase evidence were used. Items marked UNVERIFIED could not be
confirmed from non-test source.*