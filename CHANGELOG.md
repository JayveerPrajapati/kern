# Changelog

All notable changes to kern are documented here. Format follows
[Keep a Changelog](https://keepachangelog.com/en/1.1.0/), and this project
adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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