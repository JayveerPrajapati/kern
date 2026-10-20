# Security Policy

kern is a local, deterministic code-intelligence engine for AI agents. It runs
entirely on your machine: zero network calls by default, no telemetry, no
phoning home. This document describes its security model, supported features,
known limitations, and how to report vulnerabilities.

## Reporting a Vulnerability

- **Preferred:** use GitHub's private vulnerability reporting for this
  repository (the "Security" tab → "Report a vulnerability"), or open a
  private advisory. Include the affected version, a minimal repro, and impact.
- **Email:** if you prefer email, send details to the maintainers via the
  contact address listed on the repository profile. Do not include live
  secrets or credentials in the report body.
- **Responsible disclosure:** please do not open public issues for security
  vulnerabilities. We follow a coordinated disclosure process:
  - Initial triage / severity assessment within **5 business days**.
  - Fix or mitigation plan within **14 days** of confirmation.
  - Coordinated disclosure after a fix is released; private advisories are
    credited unless the reporter requests anonymity.

### Scope

- In scope: the `kern` codebase (CLI, MCP server, governance, sandbox, index),
  configuration handling, and documented env-flag security controls.
- Out of scope: third-party dependencies (report those to their own
  maintainers), user-created policies/hooks, and misconfiguration of the
  environment by the operator.

## Security Features

- **PII masking** — output is scrubbed before it reaches callers
  (`internal/pii/pii.go`); exposed to agents as the `kern_mask_pii` MCP tool.
- **Sandboxed execution** — host commands run against a filesystem snapshot
  with exact rollback on non-zero exit (`internal/sandbox/sandbox.go`).
  Per-file snapshot cap is 100 MiB (`KERN_SANDBOX_MAX_SNAPSHOT_BYTES`); files
  over the cap are not snapshotted and rollback reports data-loss risk
  instead of silently restoring.
- **Governance firewall** — agent/task scoping with approval workflows; risky
  actions require explicit approval gates before execution
  (`internal/governance/firewall.go`, `internal/governance/approval.go`).
  All host-command execution passes through `CheckExec`
  (`internal/governance/exec.go`) and fails closed.
- **Evidence bundles** — decisions and executions are recorded as
  tamper-evident bundles sealed by SHA-256 content hashes, with an optional
  ed25519 project key (`internal/evidence/`).
- **Authorized context** — governed context scoping is enforced through the
  security boundary and task-scope validation paths
  (`internal/governance/authorize.go`).
- **Security scanning** — `kern verify security` (or `kern sec`) scans for secrets, SQL
  injection, weak crypto, and unsafe deserialization (`internal/sec` + `internal/secscan`).
- **MCP tool confinement** — path-typed tool arguments are confined to
  configured roots via `withinRoot()` and `rootedPath()` in
  `internal/mcp/server.go`. Symlinks are
  resolved and rejected when they escape a root.
- **Tamper-evident audit chain** — every governed action is appended to a
  hash-chained audit log (`internal/governance/audit.go`).

## Known Security Considerations

- **Arbitrary root/dir arguments** — MCP tools accept arbitrary `root`/`dir`
  arguments. This is by design: the loopback client is the trusted principal,
  and confinement roots are enforced per tool invocation.
- **TLS for HTTP MCP transport** — the HTTP MCP mode supports TLS via `KERN_MCP_TLS_CERT`
  and `KERN_MCP_TLS_KEY`. When enabled, it is encrypted. If TLS
  is not configured, it is unencrypted and loopback-only; use stdio mode for anything beyond localhost.
- **HTTP MCP auth: Origin check by default, opt-in bearer token** — the
  loopback HTTP server authenticates requests by validating that the `Origin`
  header is a local origin (`isLocalhostOrigin`, `internal/mcp/http.go`);
  this is not a security boundary against local processes. For explicit TCP
  binds or shared hosts, set `KERN_MCP_AUTH_TOKEN` to additionally require
  `Authorization: Bearer <token>` on `POST /mcp` (constant-time compare,
  `internal/mcp/http_auth.go`; `/health` stays open). This is deliberately
  separate from `KERN_AUTH_TOKEN` (the web-console / enterprise serve mode).
- **Releases are GPG-signed when configured** — distributions ship with
  `SHA256SUMS` checksums; when the maintainer has configured GPG keys the
  checksums and every binary are additionally signed (`SHA256SUMS.sig`,
  per-asset `.sig`, public key `SHA256SUMS.asc`). Signing is optional, so
  always verify downloads against the published sums, and check signatures
  when present (see `docs/security/signed-releases.md`).
- **Exec tools are gated by `KERN_ALLOW_EXEC`** — host command execution is
  disabled by default. Enabling it grants the agent shell access; only enable
  it in environments you trust.

- **Locally-writable state is local-trust** — the MCP tool-response disk cache
  (`~/.cache/kern/`, key prefix `mcp-toolcache-`) and the `.kern/index.json`
  symbol index are plain writable local state, trusted like any other file in
  your project. A tampered cache entry can forge the provenance envelope
  replayed with it (the audit chain attests envelopes, not output content),
  and a tampered index can skew analysis results — kern does not defend
  against a hostile local user or process. Mitigations: cached text is
  PII-masked before persisting, and entries are keyed by index identity +
  view-bound etag, so unrelated index changes rotate the key. See
  `docs/adr/0012-conditional-fetch-etags.md` for the full trust model.

## Machine-wide install

By default the installer runs `kern setup --global-only --global-rules`
(opt out with `KERN_AUTO_WIRE=0`). What that means for your machine:

- **It edits user-level files, not repos.** It registers the kern MCP server,
  hook scripts and rules in each supported agent's home config, adds a global
  git ignore entry, and sets `git config --global core.hooksPath` to
  `~/.kern/git-hooks`. No file is written inside any repository. Previews:
  `kern setup --global-only --dry-run`. A global instructions file it
  rewrites (e.g. `~/AGENTS.md`) is first backed up to `*.bak.<timestamp>`;
  `kern setup --check` lists every wired location. Undo: `kern setup --remove` (machine-wide only; it
  removes only kern-owned entries and refuses to unset a `core.hooksPath` that
  is not kern's).
- **Global git hooks run in every repo.** `pre-commit` runs
  `kern verify --staged security,architecture`; `pre-push` refuses force-push and deletion of
  `main`/`master` (`KERN_PROTECTED_BRANCHES` to change). The repo's own hooks
  are still run. A repo with its own `core.hooksPath` (e.g. husky) shadows
  them. If the kern binary is missing the commit gate fails open instead of
  blocking every commit on the machine.
- **The agent hook guard is steering, not a security boundary.** It redirects
  built-in read/grep/bash to kern tools. It fails open when kern is missing,
  and can be switched off with `KERN_DISABLED=1`, a `.kern/off` file in the
  repo, or (for a first index build) a fresh `.kern/indexing` marker. None of
  those switches are audited, and any process that can set the environment or
  write files in the repo can use them — including an agent. The enforcement
  that matters is the exec firewall and approval gates (`CheckExec`), which do
  not depend on the hook guard.
- **Git hook override is audited.** The only way past the pre-commit and
  pre-push gates is `KERN_BYPASS=1` / `KERN_ENFORCE=0`, which appends a
  record to `~/.kern/audit/bypass.jsonl` (`KERN_BYPASS_REASON` is recorded).
  `KERN_DISABLED` and marker files deliberately do **not** disable the git
  hooks. The bypass log is local, append-only by convention, and not
  tamper-evident, unlike the governance audit chain.
- **No auto-indexing of broad directories.** With kern wired globally an agent
  can start anywhere. The first automatic index refuses `$HOME`, a filesystem
  root and common documents folders (`KERN_ALLOW_BROAD_INDEX=1` overrides;
  `kern index <dir>` is always allowed and is a deliberate act).

## Build Tags

- `-tags nosqlite` — disables the persistent SQLite symbol index with WAL and
  FTS5 full-text search (`internal/index/sqlite_store.go`). The default build
  uses the SQLite index.
- `-tags notreesitter` — opts OUT of the default tree-sitter AST parsing
(~13 grammars), falling back to regex heuristics
(`internal/index/treesitter.go`). Tree-sitter is compiled in by default and
is hard-CGO, so the default build requires a C toolchain. Releases ship both:
native-platform tree-sitter `-full` assets (Linux amd64/arm64, macOS
amd64/arm64, Windows amd64 — installers probe these first) and cross-compiled
pure-Go `-tags notreesitter` builds for the remaining targets.

## Dependencies

The default build links the Go standard library plus pure-Go dependencies
(`gopkg.in/yaml.v3` for YAML policy/config parsing, and the cgo-free SQLite
driver) and compiles in the tree-sitter grammars — hard-CGO C sources, so the
default build needs a C toolchain. `-tags notreesitter` drops tree-sitter for
a fully cgo-free build (the cross-compiled release builds for non-native
targets; native platforms get the tree-sitter `-full` asset); `-tags nosqlite`
disables SQLite.

## Security-relevant environment variables

All fail closed unless noted:

| Variable | Purpose |
|---|---|
| `KERN_TOOLS` | Comma-separated allowlist of MCP tools. Exec tools must be named here (or `KERN_ALLOW_EXEC` set) to run. Empty = all tools allowed. |
| `KERN_ALLOW_EXEC` | Master switch for governed host-command execution (`CheckExec`). Unset = execution refused. |
| `KERN_ALLOW_DEPLOY` | Gates external deploy operations; production mutation is disabled unless set to `1`. |
| `KERN_MCP_ROOTS` | Comma-separated workspace roots for MCP path confinement. Defaults to the process cwd and fails closed. |
| `KERN_AUTH_TOKEN` | Bearer token required for enterprise / HTTP serve mode. Unset = server refuses to serve (503). |
| `KERN_MCP_AUTH_TOKEN` | Opt-in bearer auth for the HTTP MCP transport: when set, `POST /mcp` requires `Authorization: Bearer <token>` (constant-time compare; `/health` stays open). Unset = no bearer auth — the default `auto` transport is a 0600 unix socket, and explicit TCP binds are loopback-only with Origin checks. |
| `KERN_SANDBOX_MAX_SNAPSHOT_BYTES` | Per-file sandbox snapshot cap (default 100 MiB). |
| `KERN_MCP_TLS_CERT` | Path to the TLS certificate file for the HTTP MCP server. |
| `KERN_MCP_TLS_KEY` | Path to the TLS private key file for the HTTP MCP server. |
| `KERN_ALLOW_UNISOLATED` | If set to `1`, allows execution outside the sandbox. |
| `KERN_ALLOW_NET` | If set to `1`, allows unrestricted network access during sandbox execution. |
| `KERN_ALLOW_BROAD_INDEX` | If set to `1`, allows automatic first indexing of `$HOME`, a filesystem root or a documents folder (refused by default). |
| `KERN_BYPASS` / `KERN_ENFORCE=0` | Break-glass: lets hooks and git hooks through. **Opens** rather than closes; audited to `~/.kern/audit/bypass.jsonl`. |
| `KERN_DISABLED` | **Opens**: switches the agent hook guard off for the process. Not audited; has no effect on the git hooks. |