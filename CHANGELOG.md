# Changelog

All notable changes to kern are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- **Unified Change Governance Engine (`internal/blueprint`)**: Merged sibling `blueprint` engine natively into `kern`, integrating all 21 packages and 30 phase gates ($G_0$–$G_{29}$) without breaking contracts.
- **First-Class Subcommands**: Wired `kern check`, `kern fix`, `kern ci`, and `kern verify-receipt` directly into `cmd/kern` CLI.
- **Drop-in Compatibility Wrappers**: Provided `cmd/blueprint` and `cmd/blueprint-mcp` entry points to preserve backward compatibility for existing scripts and hooks.
- **KernOps Governed Autonomous Platform Plan**: Added `docs/architecture/kernops-plan.md` specifying the architecture, Option 2 standalone repository layout (`/workspace/kernops`), self-healing repair contracts, and implementation roadmap.

### CI & Tooling

- **Toolchain Availability in `/usr/local/bin`**:
  - Pinned and extracted official static `gitleaks` v8.30.1 directly into `/usr/local/bin/gitleaks` in GitHub Actions.
  - Globally installed `jscpd` and linked into `/usr/local/bin/jscpd`, preventing 120s `npx` network download timeouts in headless runners.
  - Pre-installed `kern`, `blueprint`, and `blueprint-mcp` to `/usr/local/bin` ahead of test execution.
  - Added a proactive `Verify tool availability` step in `.github/workflows/ci.yml`.
- **Test Robustness**: Enhanced `cmd/blueprint/g25_test.go` to ignore environment-level fallback warnings (`secret:incumbent-unavailable`) during file-level provenance validation.

## [0.9.5] - 2026-09-03

### Security

- MCP rootless path confinement: strictly validate relative paths in `rootedPath` via `withinRoot(cwd, p)` when `root` argument is omitted, blocking directory traversal (`../../`) attacks.
- Non-regular file protection: `WalkDir` callbacks in serial/parallel index builds and security scanner (`sec.Scan`) skip FIFOs, named pipes, sockets, and character devices, preventing indefinite kernel `read()` hangs.
- File read memory bounding: added `MaxReadFileBytes` (10MB) and regular file checks to `code.ReadFile`, safeguarding `kern_compact_file` and `kern_context` against heap exhaustion and OOM crashes from oversized non-code files.
- Relay peer verification: verified remote peer credentials and token integrity during cross-process socket handshakes.

### Performance & Concurrency

- Merkle Tree incremental hashing: $O(\log N)$ updates for content identity computation across large workspace graphs.
- Concurrency & synchronization hardening:
  - RWMutex fine-grained locking for the governance Firewall agent registry.
  - Cooperative cancellation context propagation in multi-agent feedback loops (`internal/loop`).
  - Worker pool bounding in semantic search to prevent goroutine explosion.
  - Synchronized single-connection pooling for modernc SQLite backends.
  - Channel semaphore backpressure during high-throughput event bus publishing.
- Monorepo ignore crawler optimization: pruned standard build/dependency directories (`node_modules`, `vendor`, `build`, `dist`, `target`, `.venv`, `.next`, `__pycache__`) in `ignore.Load`, eliminating redundant tree walks and ignore rule quota exhaustion.
- Git submodule & worktree compatibility: properly recognized `.git` and `.kern` regular pointer files in filesystem walkers.

### Fixed

- `kern commitmsg`: eliminated interactive terminal stdin blocking via `os.ModeCharDevice` check; auto-detected and prioritized staged diffs (`git diff --cached`); expanded stopword filtering and package-level scoping to remove single-letter and Go keyword noise.
- Go formatting compliance: ran `gofmt` repo-wide to satisfy CI automated formatting gate.

## [0.9.4] - 2026-09-03

### Added

- Live guard-event streaming: `relay.PublishPersisted` dual-leg (durable append
  + live socket emit) wired into the CLI guard path and MCP `handleGuardCheck`
  (G-1).
- Audit-chain coverage for every MCP tool call: one chained entry per dispatch
  via `toolAudit`, lazily persisted to `.kern/audit` (G-2); O(1) JSONL appends
  for the chain; cross-process writer serialization + tamper-chain repair.
- Sandbox network-policy audit on the impact manifest (netns state,
  KERN_ALLOW_NET / KERN_ALLOW_NO_ISOLATE, network-error signatures) (G-3);
  post-run impact manifest with content hashes.
- Range-scoped taint generation + Python sink scanning (7 rules) with pytest
  scaffolds (G-4).
- Cache maintenance: TTL eviction + gzip archival of dormant entries, `kern
  cache` CLI (G-7).
- Minimal stdio JSON-RPC LSP server: `kern lsp` — hover, definition,
  references (G-8).
- MinHash LSH banding for large-log clustering + near-duplicate log-line
  clustering with repetition counts (G-9).
- Duplication-debt cleanup (G-11): canonical `index.LoadOrBuild` and
  `runtime.FormatEvent`; itoa/dirMatch/recorder-wiring families extinct
  repo-wide; CLI<->MCP default-agent twins consolidated.
- Incremental index rebuilds with prior-index reuse; parallel index build with
  deterministic merge.
- Cross-process event relay over `.kern/events.sock`.
- Declarative custom agent adapters (`.kern/agents.json`).
- Model-aware tokenizer hot-swap with exact cl100k/o200k BPE.
- MCP draft-code validation + taint-lite analysis tools.
- `kern calibrate` — F1 calibration harness exposed as a CLI command.
- Watch: trailing debounce with dependency-aware rebuild wait.
- Guard: `@pure` mutation assertions and violation event publishing.
- Diff: compact view-only diffs with symbol span annotations.
- Budget: AST-aware code fitting that preserves signatures.
- What-if: flags broken call sites, untested symbols, boundary violations.

### Fixed

- `storage.LogStore` de-flaked: deterministic Put order in TestLogStoreCRUD.
- `execution.Worktree.Diff` tolerates unhashable files.
- Audit chain repair for cross-process writers.

### Docs

- Corrected stale tool counts and the "read-only" server claim (G-6); G-11
  tracker marked DONE; next-plan gap surfaces documented.

## [0.9.0] - 2026-09-01

### Added

- Governance as the authz spine: `authorize-context` (P0.1 authorized-context
  primitive), freshness, and isolation checks; evidence bundle (P1.1) with
  tamper-evidence seal; phase-aware MCP tool catalog.
- `kern fingerprint` structural fingerprint subcommand with control-flow.
- `kern_authorize_context` MCP tool (84th tool; plugin parity maintained).
- `KERN_MCP_ROOTS` pre-tool confinement gate for MCP tool calls.
- Versioned machine-readable JSON output (`schema_version: 1`) for
  `kern guard` and `kern security`.
- Java package import extraction for boundary checks.
- `kern_meta` meta-tool (83rd MCP tool): single natural-language entry point
  with a deterministic classifier, plus CLI mirror `kern meta`.
- `kern_approve` MCP tool to resolve governance approval gates.
- Pre-tool MCP hook + `--json` output on CLI commands.
- Global-first agent setup with lazy index and exposure cleanup.
- kern-server shipped as the third release binary (tarballs, brew, install.sh).
- CI gate on push to main (`go vet` + `go test` + binary builds).

### Fixed
- Audit log replay now restores entries in numeric write order instead of
  lexical store key order, so the tamper-evident chain verifies for logs with
  10+ entries (previously the chain always appeared broken after replay).
- Audit log tamper warning is now migration-aware: entries written by an older
  kern version (unverifiable even against an empty chain head) produce a calm
  NOTE; a mid-chain break still produces the loud tamper WARNING.
- Guard fail-open: import-level violations now attribute to the importing file.
- Guard: MCP server protected from preload panics; index cached in
  high-level handlers.
- Approval FileStore writes are atomic (lost-update race).
- JSON store writes serialized across instances (lost-update race).
- MCP HTTP transport nil-map panic on commits/sem.
- Enterprise agent registration via POST /org/agents.
- Declaration-aware commit message classification.
- Direct task execute fixed; governance enforcement wired across agents.
- sandbox snapshots now skip `.blueprint/` (validation metrics).
- Plugin timeout handling: the `bash` and `kern_run_build` shadows now honor the
  agent-provided timeout (milliseconds) up to a 30-minute cap instead of a hard
  120s ceiling, so long builds/tests no longer die with a misleading
  "kern timed out after Nms" (the governed path previously converted the agent
  timeout to ~1s, then the raw fallback re-killed it after N ms). The timeout
  unit is documented on both tools.
- Removed three duplicate dead tool definitions in the plugin registry
  (`kern_correlate`, `kern_learn`, `kern_modernize` were each defined twice;
  the last copy won at runtime, the first was dead code).

### Fixed (cross-surface pitfall audit, 2026-08-31)
- Plugin shadows `kern_exec`/`kern_heal`/`kern_validate`/`kern_sandbox` now
  convert `timeout` (ms) to CLI seconds AND forward the budget to the runner —
  previously the agent's timeout was silently discarded and every call was
  hard-capped at 120s (`kern_run_build`/`bash` were already fixed).
- `kern_exec` shadow preserves the real exit status: a failing script now
  surfaces as a failure with its output instead of a forced-success.
- Bash shadow's raw fallback now triggers ONLY when the kern binary is
  missing: no more double-execution of a command that timed out governed, and
  no firewall-bypass re-run when a denial is phrased outside the keyword
  regex. `glob`/`grep` shadows route patterned/include-filtered queries to the
  raw fallback instead of silently dropping the pattern.
- MCP per-call context now has a 10-minute deadline (was unbounded — a hung
  Ollama or slow index build could wedge the server goroutine forever).
- `kern_run_build` returns partial build output on timeout/failure instead of
  discarding it; `kern build` (CLI) prints the partial output plus a
  `--timeout` hint instead of a bare error.
- `kern exec --timeout 0` now means "no limit" (was silently coerced to the
  script runner's 10s default), matching the `toolTimeout` contract used by
  build/validate/heal/sandbox.
- Output sandbox truncation is rune-safe (no dangling UTF-8 sequences); build
  log and log-compression caps append explicit "… (N lines omitted)" markers;
  `kern_learn` rejects malformed `threshold` instead of silently defaulting;
  `kern_safe_delete --json` surfaces marshal errors.

### Changed (breaking)
- **MCP confinement is now default-on (fail-closed to cwd).** Previously
  (v0.8.0), an unset `KERN_MCP_ROOTS` meant loopback trust (no path
  confinement). kern now confines MCP tool file/path arguments to the current
  working directory by default. MCP tools that legitimately read outside cwd
  (e.g. `~/.config`, home-dir docs) will return "pre-tool use denied" errors
  after upgrading. To restore the old permissive behavior, set
  `KERN_MCP_PERMISSIVE=1`. To allow specific roots, set
  `KERN_MCP_ROOTS=/path/one:/path/two`.

### Changed
- Release tarballs (`make release`, release.yml, Homebrew) all build with
  `-tags sqlite` (pure-Go, CGO_ENABLED=0 safe) for feature parity.
- README install docs now cover all three binaries (`kern`, `kern-mcp`,
  `kern-server`).
- Intent capability registry with runtime evidence and resumable blocked
  tasks; `kern_run`/`kern_onboard` MCP tools exposed.

## [0.8.0]

Previous release. See the tag for the full change set.
