# Kern: Universal Autonomous Governance & Agent-First Operating Engine

**Author/Target:** Kern Core Architecture & Universal Agent Autonomy  
**Status:** Master architecture specification & implementation roadmap — corrected 2026-09-28 against the implemented code (sections marked ⚠️ describe design intent that is NOT implemented as written)  
**Target Repository:** this repository (the kern monorepo; file links are repo-relative)  
**Core Authority:** `kern` is the sole source of truth. (Derived directly from production source code).

**Implementation status (audited 2026-09-28, verified against the code):**
- ✅ §2.1 global recognition — implemented: global MCP registration for 10+ agents, six research-verified global rules hosts (`internal/setup/globalrules.go`), machine-wide git gate (`internal/bpcli/cli/install_global_git.go`).
- ⚠️ §2.2 runtime interception — the MCP-internal Pre-Tool Interceptor and Post-Tool Optimizer are **impossible as written**: an MCP server receives calls only for its OWN tools; it cannot intercept the host agent's native read/grep/bash or rewrite other tools' outputs. Interception lives host-side: PreToolUse hooks (kern-guard.sh) wired for Claude/Cursor/Gemini/Copilot/Antigravity, plus the opencode in-process plugin.
- ✅ §2.2 reactive file watcher — implemented as a polling index-freshness watcher (`internal/mcp/watcher`, opt-in via `KERN_MCP_WATCH_INTERVAL_MS`): cheap FreshnessProof, rebuild into a NEW index, atomic swap + cache invalidation. No fsnotify dependency; no quarantine.
- ✅ §6 emergency break-glass — implemented env-only and audited (marker-file and payload-grep variants removed as security holes).

---

## 1. The Core Philosophy: "Install Once, Governed Everywhere"

Universal Agent Coverage must **not** be limited to passive security hooks or pre-commit checks.

Once `kern` is installed or running via MCP, **Kern becomes the default operational brain for all AI agents on the machine across all tasks**:
1. **Exploration & Navigation:** Agents automatically query `kern_explore`, `kern_compact_file`, and `kern_project_map` instead of burning 100k+ tokens reading raw files.
2. **Search & Intelligence:** Agents automatically use `kern_search` and `kern_ast_search` instead of noisy, blind regex greps.
3. **Planning & Blast-Radius:** Agents automatically run `kern_plan` and `kern_impact` to assess transitive breakage before modifying code.
4. **Execution & Sandboxing:** Agents execute commands through `kern_validate` and `kern_exec` in network-isolated sandboxes instead of running arbitrary host shell commands.
5. **Quality & Governance:** All edits automatically clear Gates G0–G39, updating the tamper-evident audit ledger with zero manual prompts.

---

## 2. Universal Agent Autonomy Architecture

```
                                  [ DEVELOPER MACHINE ]
   ═════════════════════════════════════════════════════════════════════════════════════
   1. GLOBAL RECOGNITION (Install-Once Bootstrap)
   Running `kern setup` or `kern install` installs Kern into every agent's global profile:
     ├── Global MCP Server     : Registered in Cursor, Claude, Antigravity, Gemini,
     │                           Windsurf, Continue, Zed, Copilot, Codex, Qwen, Qoder
     ├── Universal Rules Engine: Injects "Kern-First" directives into global agent memories
     │                           (~/AGENTS.md, ~/.cursor/rules, ~/.claude/CLAUDE.md, etc.)
     └── Global Git Engine     : `core.hooksPath` gates ANY repository machine-wide
   ─────────────────────────────────────────────────────────────────────────────────────
2. RUNTIME ACTIVE INTERCEPTION & BOOTSTRAP (When kern-mcp starts)
├── Auto-Index Workspace  : Preloads the persisted index at session start
│                           (sub-second warm cache; seconds-to-tens-of-seconds
│                           cold builds on large repos)
├── Host-Side Interception: PreToolUse hooks (kern-guard.sh) gate native tools
│                           in Claude/Cursor/Gemini/Copilot/Antigravity; the
│                           opencode plugin intercepts in-process. An MCP server
│                           cannot intercept host-native tools (protocol limit).
├── Reactive File Watcher : Polling index-freshness watcher (opt-in env): rebuild
│                           into a new index + atomic swap + cache invalidation
└── Post-Tool Compression : Host-side only (opencode plugin output compression,
│                           Claude PostToolUse hooks) — same protocol limit
   ─────────────────────────────────────────────────────────────────────────────────────
   3. CLOSED-LOOP REPAIR & EMERGENCY BREAK-GLASS
     ├── Normal Mode           : Violations emit structured Repair Contracts for self-healing.
     └── Break-Glass Override  : Bypasses blocks during emergencies with auditable trail.
   ═════════════════════════════════════════════════════════════════════════════════════
```

---

## 3. Deep Source-Code Architecture of Kern MCP (`internal/mcp/`)

A direct audit of Kern's pure production Go implementation in [`internal/mcp/`](internal/mcp) reveals an enterprise-grade control plane engineered for high-concurrency, offline agent interaction:

### 3.1 The MCP Server Core Engine ([`server.go`](internal/mcp/server.go))
The server runs over standard JSON-RPC 2.0 (`stdio` transport) with zero external network dependencies:
* **Concurrency & Platform Locking (`platformLocks`, `sem`):** Prevents concurrent calls from racing to rebuild full AST indexes. Uses a semaphore channel `sem` and per-root platform mutexes to single-flight heavy index builds while serving cached queries concurrently.
* **Path Confinement Gate (`Gate` / `server_paths.go`):** Resolves symlinks and strictly restricts all `root`, `dir`, and path arguments to allowed workspace roots (`KERN_MCP_ROOTS`). Traversal outside allowed roots fails closed.
* **Safety Budget Gateway (`ToolGateway` / `domain.SafetyBudget`):** Evaluates every tool invocation against strict resource ceilings:
  - `MaxToolCalls` (call volume limiter)
  - `MaxFiles` (file footprint ceiling)
  - `MaxTokens` (cumulative token expenditure)
  - `MaxExternalCalls` & `MaxRuntimeSeconds` (execution timeouts)
  - `MaxRisk` (blocks unpermitted high-risk commands)
* **Output Sandboxing & Secret Masking ([`maskedOutput`](internal/mcp/exec/exec.go#L64-L75)):**
  - All tool outputs are capped at 24KiB (default) or 64KiB (extended).
  - **Critical Invariant:** PII and secret redaction (`pii.Mask`) executes **before** byte-truncation, guaranteeing that secrets straddling the truncation cutoff never leak into the LLM context.
* **Content-Addressable Deterministic Cache ([`tool_cache.go`](internal/mcp/tool_cache.go)):**
  - Emits sub-millisecond cached responses for identical queries when inputs and git tree hashes match.
* **Tamper-Evident Audit Ledger ([`internal/governance`](internal/governance)):**
  - HMAC-chained audit records (`internal/governance/audit.go`; chain secret at `~/.cache/kern/audit-chain.key`). Bypass events additionally land in `.blueprint/audit/audit.jsonl` + the `.kern/audit` governance chain, and machine-wide hook-level bypasses in `~/.kern/audit/bypass.jsonl`.

---

### 3.2 The 139-Tool Catalog & Phase Routing ([`catalog/tools.go`](internal/mcp/catalog/tools.go))
Kern organises its 139 tools into 4 lifecycle phases plus cross-cutting control planes:

#### Phase 1: Explore (Code Discovery & Semantic Navigation)
* `kern_meta`: Natural-language entry point; automatically classifies and dispatches commands in-process.
* `kern_explore`: Deep symbol and file exploration (call trees, callers, callees, downstream dependencies).
* `kern_search` / `kern_ast_search`: Fast symbol matching across 17 indexed languages (<10ms).
* `kern_compact_file`: Emits token-dense symbol signatures instead of verbatim file contents (70-90% token reduction).
* `kern_project_map`: Generates hierarchical structural maps of repositories.
* `kern_doc_search`: Offline documentation search over indexed local guides and specifications.

#### Phase 2: Plan (Blast-Radius & Architectural Safety)
* `kern_plan` / `kern_impact`: Computes exact downstream transitive breakages before code is touched.
* `kern_what_if`: Simulates proposed signature modifications and identifies all impacted call sites.
* `kern_arch` / `kern_guard`: Enforces architectural layer boundaries defined in `.kern/boundaries.json`.
* `kern_cycles`: Detects cyclic dependencies between packages and modules.
* `kern_fragility_hotspots`: Calculates instability and churn metrics to highlight high-risk failure hotspots.

#### Phase 3: Edit (Governed AST Mutation & Scaffolding)
* `kern_mutate` / `kern_refactor` / `kern_rename`: Performs semantic AST symbol renames and function extractions.
* `kern_synthesize_test` ([`synthtest/synthtest.go`](internal/mcp/synthtest/synthtest.go)): Generates table-driven test suites and edge-case invariants for untested AST paths.
* `kern_repair_guidance` / `kern_explain_finding`: Emits machine-readable Repair Contracts for failed checks.
* `kern_validate_proposed`: In-memory pre-write gate validation before an agent flushes edits to disk.

#### Phase 4: Verify (Diff Gates & Network-Isolated Execution)
* `kern_verify` / `kern_check`: Evaluates full phase gates G0–G39.
* `kern_diff_gate`: Dedicated diff scanners:
  - G30: Diff gofmt formatting enforcement.
  - G31: In-house security scanner (taint tracking, injection, weak crypto).
  - G32 & G35: Schema drift and MCP catalog drift detection.
  - G33: Diff unsafe execution detection (`os/exec`, `exec.Command`, `sh -c`).
* `kern_validate` / `kern_exec` ([`exec/validate.go`](internal/mcp/exec/validate.go)):
  - Executes build/test commands inside an ephemeral Git worktree.
  - Network isolation: Linux network namespaces (`CLONE_NEWNET`) and macOS `sandbox-exec` network-deny profiles.
  - Process group timeouts with SIGKILL cleanup on cancellation.

#### Cross-Cutting: Squad Control, Memory & Flight Observability
* `kern_agents` ([`handlers_agents.go`](internal/mcp/handlers_agents.go)): 7-role specialist squad (Planner, Architect, Coder, Reviewer, Security, Tester, SRE).
* `kern_memory_recall` / `kern_memory_add` ([`memory/memory.go`](internal/mcp/memory/memory.go)): Engineering memory storage and ranked semantic recall.
* `kern_flight` ([`handlers_flight.go`](internal/mcp/handlers_flight.go)): AI flight recorder tracking agent reasoning steps, tool payloads, and verification outcomes.
* `kern_loop` ([`internal/loop/loop.go`](internal/loop/loop.go)): L0–L5 closed autonomous development loop.

---

### 3.3 Host Agent Model Delegation via MCP Sampling ([`sampling.go`](internal/mcp/sampling.go) & [`mcp_provider.go`](internal/llm/mcp_provider.go))
Kern eliminates external API key dependencies and heavy GPU requirements by borrowing the active agent's LLM directly:
1. **Dynamic Session Handshake:**
   * When an MCP client initializes, Kern inspects client capabilities. If sampling is announced, Kern binds that session:
     ```go
     disp := llm.RegisterHostSamplerFor("mcp-conn-1", s.sample)
     ```
2. **Reverse RPC Request (`sampling/createMessage`):**
   * When Kern needs generation (synthesizing tests, analyzing architectural risk), it sends a JSON-RPC request back to the host IDE:
     ```json
     {
       "jsonrpc": "2.0",
       "method": "sampling/createMessage",
       "params": {
         "systemPrompt": "You are Kern's repair synthesizer...",
         "messages": [{ "role": "user", "content": { "type": "text", "text": "..." } }]
       }
     }
     ```
3. **Multi-Session Isolation:**
   * Each connected IDE window or agent gets a unique connection key (`mcp-conn-1`, `mcp-conn-2`). When an agent closes, its disposer unregisters that slot, preventing zombie handles.

---

## 4. Universal Auto-Wiring Matrix: Making Kern Default for All Agents

During `kern setup` or on `kern-mcp` launch, Kern automatically writes global registrations so **every agent on the system adopts Kern by default**:

### 4.1 Global MCP Registration Matrix
| Agent Host | Global Configuration Target | Parameters Auto-Wired |
| :--- | :--- | :--- |
| **Antigravity (`agy`)** | `~/.gemini/config/mcp_config.json` | `kern-mcp`, `KERN_ALLOW_EXEC=1` |
| **Claude Code** | `~/.claude/settings.json` | Global stdio MCP server |
| **Cursor** | `~/.cursor/mcp.json` | Global stdio MCP server |
| **Gemini CLI** | `~/.gemini/settings.json` | `mcpServers` block |
| **Windsurf** | `~/.codeium/windsurf/mcp_config.json` | Pre-registered `kern-mcp` |
| **Continue** | `~/.continue/config.json` | `mcpServers` block |
| **Zed** | `~/.config/zed/settings.json` | `context_servers` entry |
| **Copilot / VS Code** | Global config or `.vscode/mcp.json` | Stdio server integration |
| **Codex, Qwen, Qoder** | `~/.codex/`, `~/.qwen/`, `~/.qoder/` | Automated JSON wiring |
| **OpenCode** | Global config `~/.config/opencode/` | Native TypeScript plugin |

---

### 4.2 Global Rule & Directive Injection
Injects the mandatory Kern-first policy into the six research-verified global instruction slots (`internal/setup/globalrules.go`, verified 2026-09-28 against official docs and gemini-cli source): `~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.config/opencode/AGENTS.md`, `~/.cursor/rules/kern.mdc` (machine-local, no sync), `~/.gemini/GEMINI.md` (NOT `config/GEMINI.md`), and `~/.codeium/windsurf/memories/global_rules.md` (NOT `~/.windsurf/rules/` — that is the legacy workspace location; whole-file 6000-char cap). No mainstream agent natively reads a home-root `~/AGENTS.md`; each relocates to its own config dir:
```markdown
# Kern-First Operational Directives (Enforced Machine-Wide)
Kern tools are ALREADY INSTALLED, 100% offline, and available via MCP:
- NEVER call built-in file reading before checking `kern_compact_file` or `kern_explore`.
- NEVER run raw regex grep before querying `kern_search` (AST symbol search).
- NEVER make multi-file edits without running `kern_impact` or `kern_plan` to inspect blast radius.
- NEVER run raw bash commands to test/build; call `kern_validate` to execute in a governed sandbox.
- When in doubt, call `kern_meta(request="...")` to let Kern classify and route your task automatically.
```

---

### 4.3 Universal Lifecycle Hooks (Interception Layer)
| Agent Host | Configuration Location | Hook Type | Triggered Kern Action |
| :--- | :--- | :--- | :--- |
| **Antigravity** | `~/.gemini/config/hooks.json` | `PreToolUse`<br>`PostToolUse` | Blocks naive `view_file`/`run_command`; runs diff check on `write_to_file`. |
| **Claude Code** | `~/.claude/settings.json` | `PostToolUse`<br>`UserPromptSubmit` | Compresses bloated outputs; captures intent prompt into memory. |
| **Cursor** | `~/.cursor/hooks.json` | `preToolUse` | Blocks `Read\|Grep\|Glob\|Bash`, redirects to `kern_*`. |
| **Gemini CLI** | `~/.gemini/settings.json` | `BeforeTool`<br>`AfterTool` | Replaces raw reads; compresses logs. |
| **Copilot** | `~/.copilot/hooks/kern-pretooluse.json` | `PreToolUse` | Enforces governed execution. |
| **OpenCode** | `~/.config/opencode/plugins/kern.ts` | In-process interception | Transparently routes built-ins directly to Kern AST index. |

---

## 5. Critical System Upgrades to Complete Autonomous Operation

### 5.1 Anti-Bypass Shield (Machine-Wide Pre-Commit Gate)
Implemented as the global git engine: `git config --global core.hooksPath` (`~/.kern/git-hooks`) runs `kern check --staged --fast` before every commit in every repository, chaining any repo-local hooks after itself. Honest limit: `git commit --no-verify` still skips git hooks entirely — that is git-inherent and cannot be neutralized from the hook layer; the audited break-glass (§6) and the host-side PreToolUse hooks (which can block the command string in gated agents) are the compensating controls.

### 5.2 Greenfield & Empty Workspace Resilience
When zero indexable source files exist in a directory, Kern returns:
```text
[kern] Greenfield project detected: AST engine initialized. Ready for initial code creation.
```
Prevents agents from aborting or erroring during the first turn of project scaffolding.

### 5.3 Incremental SQLite Cache for Cross-Mount I/O (WSL/Windows)
Persists symbol and AST tables incrementally in `.kern/index.db` keyed by file `mtime` and size. Cold index loading drops from seconds to **<15ms**.

### 5.4 The Autonomous Closed Loop: Self-Learning & Proactive Memory Push
Solves the "Pull vs. Push" dilemma where agents forget to call memory tools:
1. **Proactive Memory Injection (Push, Not Pull):** Any retrieval tool (`kern_meta`, `kern_explore`, `kern_search`) automatically attaches matching historical lessons to the top of its response.
2. **Session-Start Digest (`PreInvocation` Hook):** Injects recent co-changes and architectural constraints into the agent's initial prompt turn.
3. **Cross-Agent Memory Synchronization:** Lessons learned in Cursor are automatically saved into `.kern/memory/lessons.jsonl` and pre-loaded when opening Antigravity or Claude Code.

### 5.5 Host LLM Cognitive Augmentation for Governance Lifecycles
Kern combines deterministic static checks with the active session's host LLM (via `sampling/createMessage`):
1. **Intelligent Risk & Two-Person Approval Classification (Gate G29):** Assesses true architectural risk on surgical AST blast-radius slices.
2. **Context-Aware Repair Guidance:** Synthesizes idiomatic refactoring solutions into Repair Contract JSONs.
3. **Intent-to-Diff Alignment:** Validates that the patch satisfies the developer's original prompt without phantom side-effects.

---

## 6. Emergency Bypass Protocol (Environment Variable Break-Glass)

During mission-critical P0 production emergencies, gates can be bypassed instantly without fighting the engine:

```
                       EMERGENCY BYPASS TRIGGER
       ┌────────────────────────────────────────────────────────┐
       │   KERN_BYPASS=1  or  KERN_ENFORCE=0                    │
       │   (Optional: KERN_BYPASS_REASON="P0 hotfix...")        │
       └───────────────────────────┬────────────────────────────┘
                                   │
                                   ▼
       ┌────────────────────────────────────────────────────────┐
       │                   KERN GATE EVALUATOR                  │
       │ - Detects KERN_BYPASS=1 or KERN_ENFORCE=0 in env       │
       │ - Downgrades hard BLOCK to advisory WARN               │
       │ - Writes a Kind=bypass audit record (reason, the      │
│   gates that would have fired)                           │
       └───────────────────────────┬────────────────────────────┘
                                   │
                                   ▼
       ┌────────────────────────────────────────────────────────┐
       │                  AUDIT & VISIBILITY                    │
       │ - Allows Git Commit / Tool Execution                   │
       │ - Audit chain: .blueprint/audit/audit.jsonl +          │
│   .kern/audit; hook-level: ~/.kern/audit/bypass.jsonl │
       │ - Prints High-Visibility Warning Banner                │
       └────────────────────────────────────────────────────────┘
```

1. **Single-Command Hotfix:**
   ```bash
   KERN_BYPASS=1 KERN_BYPASS_REASON="P0 prod incident #1042" git commit -m "fix: emergency db failover"
   ```
2. **Session-Level Hotfix:**
   ```bash
   export KERN_BYPASS=1
   # Perform emergency repairs...
   unset KERN_BYPASS
   ```
3. **Agent Pre-Tool Guard Bypass:**
   Setting `KERN_ENFORCE=0` causes `kern-guard.sh` to immediately yield `exit 0`.
4. **Mandatory Audit Logging:**
Even during emergency bypasses, Kern writes a `Kind="bypass"` audit record (reason + the gates that would have fired) into the `.blueprint/audit/audit.jsonl` HMAC chain and the `.kern/audit` governance chain; the global git hook appends machine-wide entries to `~/.kern/audit/bypass.jsonl`, and a high-visibility banner is always printed. Env-only by design: earlier marker-file (`/tmp/kern-bypass`, `~/.kern/bypass`, `.kern/bypass`, `BLUEPRINT_BYPASS`) and payload-grep bypass variants were removed as security holes — any agent could have silently disabled governance by touching a file or naming the variable in a payload.

---

## 7. Verification Runbook

1. **Verify Global Git Gate:**
   ```bash
   kern setup
   cd /tmp && mkdir test-repo && cd test-repo && git init
   echo 'AWS_SECRET="AKIAIOSFODNN7EXAMPLE"' > secret.txt
   git add secret.txt
   git commit -m "test commit" --no-verify
   # Outcome: Kern blocks the commit and redacts the secret.
   ```
2. **Verify Emergency Environment Bypass:**
   ```bash
   KERN_BYPASS=1 KERN_BYPASS_REASON="P0 hotfix" git commit -m "emergency commit"
   # Outcome: Commit succeeds with high-visibility warning banner and audit record.
   ```
3. **Verify Greenfield Mode:**
   ```bash
   cd /tmp && mkdir empty-project
   kern meta "find main function" --root /tmp/empty-project
   # Outcome: Returns clean status without crash.
   ```
4. **Verify Host Sampling (Antigravity/Cursor):**
   * Trigger a test synthesis or complex repair in Kern without local Ollama running.
   * Kern calls `sampling/createMessage` to the active agent session, returning frontier LLM generation directly.

---

## 8. Summary

With this master architecture:
* **Kern is the authoritative, self-contained source of truth.**
* **Pure zero-touch adoption:** Installing or booting `kern-mcp` automatically arms every agent and Git repository machine-wide.
* **Proactive closed loop:** Pushes memory and context directly into agent streams so they never operate blindly.
* **Resilient break-glass:** Allows instant emergency hotfixes while preserving an immutable cryptographic audit ledger.
