# kern Threat Model

**Status:** Living document — update when attack surface or mitigations change
**Scope:** kern, a local, deterministic code-intelligence engine for AI coding agents
**Last reviewed:** 2026-09-09

---

## 1. Threat Model Overview

kern is a local CLI + MCP (Model Context Protocol) server tool. It runs
entirely on the machine it is installed on: zero network by default, no
telemetry, no external calls. Its purpose is to give AI coding agents
(Claude, Cursor, opencode, etc.) fast, deterministic access to a prebuilt
symbol index of a project, plus governance, execution, and verification
primitives.

### 1.1 Primary users

- AI coding agents that call kern via the MCP protocol (`tools/call`).
- Developers who invoke the `kern` CLI directly (`kern check`, `kern exec`,
  `kern audit`, `kern verify-receipt`, …).

### 1.2 Attack surface

| Surface | Description | Transport |
|---|---|---|
| MCP protocol | JSON-RPC tool calls over stdio (default) or opt-in HTTP | stdio / loopback HTTP |
| CLI commands | `kern` subcommands operating on the workspace | local process |
| File system | Reading/indexing/mutating files under the workspace root | local filesystem |
| Network | HTTP MCP mode (loopback-only), opt-in Ollama/OpenAI/doc-fetch | TCP, loopback |
| Execution | `kern_exec`, `kern_sandbox`, `kern_execute` run host commands | local process |

### 1.3 Key security properties

1. **Fails closed.** Denial, misconfiguration, or an unknown state refuses to
   run rather than allowing it (see `CheckExec`, `withinRoot`, the confinement
   `Gate`, and the audit `VerifyChainReport`).
2. **Confinement by default.** Path-typed tool arguments are confined to the
   workspace root even with zero configuration.
3. **Opt-in power.** Arbitrary host command execution requires explicit
   opt-in (`KERN_ALLOW_EXEC=1` or a `KERN_TOOLS` allowlist naming the exec
   tool) and passes a risk firewall.
4. **No implicit trust.** `KERN_MCP_PHASE` is explicitly documented as *not*
   a security boundary — only the `KERN_TOOLS` allowlist is
   (`internal/mcp/server.go`, comment on the phase constants).

---

## 2. Attack Vectors

### 2.1 MCP Protocol

A malicious or compromised MCP client (or an agent under prompt injection)
sends tool calls over JSON-RPC. Because MCP phase filtering only changes
*advertisement* (`tools/list`), any client that knows a tool name can call it
directly — so every handler must enforce its own constraints.

| Vector | Description | Example |
|---|---|---|
| Tool call injection | Agent under prompt injection calls a powerful tool it should not use | Calling `kern_sandbox` with a destructive command |
| Path traversal | Path-typed arguments containing `..` or absolute paths outside the workspace | `path: "../../etc/shadow"` |
| Symlink escape | A symlink inside the workspace pointing outside it | `link -> /etc` then `read(link/passwd)` |
| Tool allowlist bypass | Invoking a tool not on the `KERN_TOOLS` allowlist | `tools/call` with an unlisted name |

**MCP protocol details**

- Protocol version `2025-06-18` (`internal/mcp/server.go`).
- Default transport is stdio; HTTP is opt-in and loopback-only
  (`internal/mcp/http.go`).
- Tool calls are rate-limited by a concurrency semaphore
  (`defaultConcurrency()`, `internal/mcp/http.go`), which bounds resource
  exhaustion from parallel calls.

### 2.2 File System

kern reads, indexes, and (via edit/exec tools) mutates files. The trust
boundary is the workspace root; everything else is attacker-controlled input
from the client.

| Vector | Description |
|---|---|
| Path traversal | `../../` or absolute paths escaping the project root |
| Symlink escape | Reading through a symlink that points outside the root |
| Rootless absolute path | An absolute path passed without a `root` argument (rejected outright — `internal/mcp/server.go`, `rootedPath`) |
| Unbounded reads | Large generated files consumed without a size cap (addressed by the sandbox snapshot cap) |

### 2.3 Execution

`kern_exec`, `kern_sandbox`, and `kern_execute` run arbitrary host commands.
This is the highest-impact surface: a successful attack is arbitrary code
execution as the user running kern.

| Vector | Description |
|---|---|
| Arbitrary command execution | Client causes `kern_exec` to run a hostile command |
| Shell injection | Command string assembled from attacker-controlled input |
| Sandbox escape | Command in `kern_sandbox` modifies files outside the snapshot (addressed by snapshot/rollback + confinement) |

### 2.4 Network

By default kern makes **no** network calls. Opt-in network surfaces:

| Vector | Description |
|---|---|
| HTTP MCP mode | Loopback-only binding with Origin-header checks; **optional TLS** (`--tls-cert/--tls-key` or `KERN_MCP_TLS_CERT`/`KERN_MCP_TLS_KEY`); plain HTTP by default |
| LLM providers | Opt-in calls to a local Ollama (`http://localhost:11434`, `internal/llm/llm.go`) or a configured OpenAI-compatible endpoint (`OPENAI_BASE_URL`) |
| Doc fetch | Opt-in `kern_doc_fetch`; loopback fetches require `KERN_ALLOW_LOOPBACK_FETCH=1` (`internal/fetch/fetch.go`) |

### 2.5 Supply Chain

| Vector | Description |
|---|---|
| Dependencies | Third-party Go modules compiled into the binary |
| Build artifacts | Tampering with a released binary or its receipts |
| Release signing | No GPG signing of releases today (see Residual Risks) |
| Receipt verification | `kern verify-receipt` validates SARIF / in-toto attestations and the local audit-chain hash (`internal/blueprint/cli/verify_receipt.go`, `internal/blueprint/receipt/`) |

---

## 3. Mitigations (what kern does)

### 3.1 Filesystem confinement

Every path-typed tool argument is resolved and confined before a handler
runs.

- **`withinRoot(root, file)`** — `internal/mcp/server.go:1423`. Resolves the
  candidate against the root, rejects `..` escapes and absolute paths outside
  the project, and resolves symlinks on **both** the root and the candidate
  (`filepath.EvalSymlinks`) so a symlink inside the project that points
  outside cannot read/escape the boundary. Falls back to a lexical
  `Clean + Rel` check when a file does not exist yet.
- **`rootedPath(root, p)`** — `internal/mcp/server.go:1250`. Wraps
  `withinRoot`; a rootless call may only reference a path relative to the
  cwd, and an absolute path without a root is rejected outright (otherwise a
  caller could pass `path=/etc/shadow`).
- **`Gate` / `NewGateFromEnv()`** — `internal/mcp/gate.go`. The pre-tool-use
  confinement gate (`srv.preTool = srv.gate.Check`, `internal/mcp/http.go`).
  Confines every tool call's `root`, `dir`, and `*path*` arguments to the
  `KERN_MCP_ROOTS` (or `mcp.roots`) list; **fails closed to the process
  working directory when unset**, so zero-config deployments are confined to
  the workspace. Roots are symlink-resolved once (`symlinkOrSelf`). Opt-outs
  are explicit and documented: `KERN_MCP_PERMISSIVE=1` (gate) and
  `KERN_MCP_NO_CONFINE=1` (`confinementGate()`, `internal/mcp/server.go:477`).
- **`Escape(root, p)`** — `internal/sandbox/sandbox.go:613`; used by the
  sandbox to reject files that escape the snapshot root.

Callers: `internal/mcp/handlers_context.go:35`,
`internal/mcp/handlers_exec.go:109-113`.

### 3.2 Execution governance

All host-command execution funnels through one gate that fails closed:
**`CheckExec(toolName ...string)`** — `internal/governance/exec.go:41`.

Three independent gates apply:

1. **Allowlist opt-in gate.** An unset `KERN_TOOLS` means "all tools
   allowed", so exec is *refused* unless the operator opts in via
   `KERN_ALLOW_EXEC=1` (`execAllowlistGate`, `internal/governance/exec.go`).
2. **Allowlist contents gate.** When `KERN_TOOLS` is set, the specific exec
   tool being invoked (`kern_exec`, `kern_sandbox`, `kern_execute`) must be
   named in it; a non-empty but unrelated allowlist does not re-enable exec.
3. **Change-firewall gate.** `command.execute` is scored by the risk model
   (`execFirewallCheck` → `Firewall.Check(agentID, "command", "execute")`,
   `internal/governance/firewall.go:128`). Severity is operator-configurable
   via `KERN_EXEC_RISK` / `exec.risk` (default `MEDIUM`;
   `internal/governance/exec.go`, `internal/config/config.go:472`). At
   `HIGH`/`CRITICAL`, execution is **approval-gated** — it fails closed until
   a human approves via `RequestExecApproval` / `ResumeExecApproval`
   (`internal/governance/exec.go`).

Callers: `internal/mcp/handlers_exec.go:32`, `cmd/kern/cmd_exec.go:31`,
`internal/execution/execution.go:56`, `internal/cicd/pipeline.go:85`.

### 3.3 PII masking

Command output and LLM-bound text are scrubbed before they leave the machine.

- **`Mask(text)`** — `internal/pii/pii.go:140`. Scans text with
  `DefaultPatterns` and replaces secrets with `[MASKED_LABEL_N]` placeholders.
- **`MaskCustom(text, patterns, names)`** — `internal/pii/pii.go:153`.
  Greedy longest-match so a URL-with-credentials is masked before its
  password is picked up.
- **`MaskAll(text)`** — `internal/pii/pii.go:159`. Also masks
  private/loopback IPs — use before sending text to a remote LLM, where even
  private IPs are PII.
- **`MaskAllCustom`** — `internal/pii/pii.go:164`.

Applied to execution output at `internal/mcp/handlers_exec.go:72` and `:335`
(`pii.Mask(out).Text`); exposed to clients as `kern_mask_pii`.

### 3.4 Sandbox (snapshot / rollback)

`kern_sandbox` snapshots the tree before running a risky command and rolls
back on failure.

- **`Snapshot(root)`** — `internal/sandbox/sandbox.go:151`; **`Restore()`** —
  `internal/sandbox/sandbox.go:345`; **`Run(...)`** —
  `internal/sandbox/sandbox.go:516`. On non-zero exit the tree is restored
  exactly: modified files restored, new files removed.
- **100 MiB per-file snapshot cap** — `maxSnapshotBytes = 100 << 20`
  (`internal/sandbox/sandbox.go:31`). A file over the cap is not copied into
  the snapshot; files skipped are surfaced via `Snap.skippedOverCap` /
  `Result.SkippedFiles`, **never silently dropped**, and rollback refuses
  (with a clear error) when a skipped file was modified or deleted so data is
  never silently lost. The cap is configurable per-invocation via
  `KERN_SANDBOX_MAX_SNAPSHOT_BYTES` (`snapshotCap()`,
  `internal/sandbox/sandbox.go:38`).
- The heal loop (`internal/heal/heal.go`) applies fixes in a throwaway
  snapshot and never edits the user's working tree.

### 3.5 Governance firewall and audit chain

- **Firewall** — `internal/governance/firewall.go`. `Check(agentID,
  resource, action)` evaluates every governed action against policies and the
  agent's permissions, returning risk plus an optional approval requirement.
  Approval workflow: `internal/governance/approval.go`
  (`ApprovalWorkflow.Request/Approve/Reject/Resume`).
- **Tamper-evident audit chain** — `internal/governance/audit.go:461`
  (`computeAuditHash`): each entry's SHA-256 hash covers the previous entry's
  hash, so modifying any entry invalidates all subsequent hashes. Persisted
  under `<root>/.kern/audit/` and verified at startup and on demand via
  `VerifyChainReport()` (`internal/governance/audit.go:534`).
- **Authorization proofs** — evidence bundles carry a SHA-256-sealed audit
  chain snapshot and authorized-scope lineage (`internal/evidence/bundle.go`);
  `kern verify-receipt` checks the audit-chain hash against the local chain
  (`internal/blueprint/cli/verify_receipt.go`).

### 3.6 Loopback-only HTTP binding

`kern-mcp --http` (streamable HTTP transport) refuses to bind anything but
the loopback interface:

- `localhostAddr()` — `internal/mcp/http.go:125`: a bare port or empty
  address binds to `127.0.0.1`; an explicitly supplied LAN IP (`0.0.0.0`,
  `::`, etc. is remapped; any other host) is **refused outright** with a
  clear error, because kern-mcp exposes RCE-capable tools and binding beyond
  loopback would be an unauthenticated network attack surface.
- Non-local `Origin` headers are rejected (`isLocalhostOrigin`,
  `internal/mcp/http.go`); empty origins (non-browser clients) are allowed.
- **Optional TLS** (`internal/mcp/http.go`): `kern-mcp --http` serves plain
  HTTP on loopback by default; passing `--tls-cert`/`--tls-key` or setting
  `KERN_MCP_TLS_CERT`/`KERN_MCP_TLS_KEY` serves HTTPS with a TLS 1.2 floor.
  An incomplete config (only one of cert/key set) is rejected — the server
  never silently downgrades to plaintext. The loopback-only bind and Origin
  checks apply regardless.
- The separate `kern-server` binary is the intended path for network access
  (default `127.0.0.1:8090`, `cmd/kern-server/main.go:43`).

### 3.7 Tool allowlist

`KERN_TOOLS` (comma-separated) restricts which tools the server executes;
parsed once at construction (`parseAllowlist`, `internal/mcp/server.go`) and
enforced per call (`toolAllowed`). Unset means everything is allowed, which
is why exec additionally requires `KERN_ALLOW_EXEC` or an allowlist naming
the exec tool.

---

## 4. Trust Boundaries

### 4.1 Local machine

kern trusts the machine it runs on: the user, the filesystem, and other local
processes are inside the trust boundary. Anything a local process can do,
kern can do — so the threat model is **not** a defense against a malicious
local process that can already read the user's files and environment.

### 4.2 MCP client ↔ MCP server

The MCP client (the AI agent host) is **semi-trusted**:

- Over stdio, the client is the parent process that spawned kern — it can
  already read the environment and send arbitrary tool calls.
- Over HTTP, the client is anyone who can reach the loopback socket. This is
  why the HTTP mode enforces loopback binding and Origin checks, and why it
  still runs the confinement gate and the exec governance gate.
- The client is assumed **not** to be able to read files outside the
  confined roots — that is the boundary the confinement gate and
  `withinRoot` enforce.

### 4.3 Agent identity and authorization

- Every tool call is associated with an agent identity; calls without an
  explicit `agent_id` fall back to a registered default agent so they are
  governed (workspace-scoped) instead of denied
  (`governance.EnsureDefaultAgent()`, `internal/mcp/http.go`).
- The firewall evaluates `agentID × resource × action` against policies;
  `kern_authorize_context` computes the exact symbol/call-edge set an agent
  may legally read (`docs/authorized-context.md`).

---

## 5. Residual Risks

| # | Risk | Notes |
|---|---|---|
| 1 | **Plaintext HTTP by default** | The HTTP MCP transport serves plaintext on loopback by default; TLS is optional (`--tls-cert`/`--tls-key` or `KERN_MCP_TLS_CERT`/`KERN_MCP_TLS_KEY`). Loopback-only binding + Origin checks mitigate, but a local attacker who can sniff loopback traffic or trick a browser into connecting to `127.0.0.1` gets unauthenticated access. Operators who need the transport outside loopback should enable TLS and put it behind an authenticated proxy; `kern-server` is the supported path for network access. |
| 2 | **No GPG signing of releases** | Binary integrity relies on the build/release process and the receipt verification (`kern verify-receipt`, in-toto/SARIF attestations). There is no cryptographic release signing today. |
| 3 | **Exec tools are powerful** | `kern_exec` / `kern_sandbox` / `kern_execute` run arbitrary host commands as the invoking user. They are opt-in (allowlist + `KERN_ALLOW_EXEC`), fail closed, and can be approval-gated at `HIGH`/`CRITICAL` risk, but a misconfigured `KERN_ALLOW_EXEC=1` + `KERN_EXEC_RISK=LOW` deployment hands an attacker code execution. Operators should keep exec risk at `MEDIUM` or above and review approval requests. |
| 4 | **Sandbox size limits** | The 100 MiB per-file snapshot cap means very large files are not snapshotted; if such a file is modified or deleted by a run, rollback cannot restore it and refuses loudly. The cap is configurable via `KERN_SANDBOX_MAX_SNAPSHOT_BYTES`, but a raised cap increases memory pressure. |
| 5 | **Symlink races (TOCTOU)** | Confinement resolves symlinks at check time; a symlink swapped between check and use is not covered. This is the standard TOCTOU limitation and is accepted for a local tool. |
| 6 | **Phase filtering is not a security boundary** | `KERN_MCP_PHASE` only changes `tools/list` advertisement; a client that knows a tool name can call it. Only `KERN_TOOLS` restricts execution. |
| 7 | **Local process trust** | Any local process with the user's privileges can already read the workspace and environment; kern adds no defense against that actor. |

---

## Appendix A: Security-relevant code map

| Concern | Location |
|---|---|
| Path confinement (`withinRoot`, `rootedPath`) | `internal/mcp/server.go:1250, 1423` |
| Pre-tool-use confinement gate | `internal/mcp/gate.go`; `confinementGate()` `internal/mcp/server.go:477` |
| Execution governance (`CheckExec`, 3 gates) | `internal/governance/exec.go:41` |
| Firewall (`Check`) | `internal/governance/firewall.go:128` |
| Approval workflow | `internal/governance/approval.go` |
| PII masking (`Mask`, `MaskAll`, `MaskCustom`) | `internal/pii/pii.go:140, 153, 159, 164` |
| Sandbox snapshot/rollback, 100 MiB cap | `internal/sandbox/sandbox.go:31, 151, 345, 516, 613` |
| Tamper-evident audit chain | `internal/governance/audit.go:461, 534` |
| Loopback-only HTTP binding | `internal/mcp/http.go:30, 125` |
| Tool allowlist (`KERN_TOOLS`) | `internal/mcp/server.go` (`parseAllowlist`, `toolAllowed`) |
| Receipt / attestation verification | `internal/blueprint/cli/verify_receipt.go`, `internal/blueprint/receipt/` |
| LLM endpoint security (https except localhost) | `internal/llm/openai_provider.go:70` |
| Loopback-only webhook hosts | `internal/webhook/webhook.go:96-109` |