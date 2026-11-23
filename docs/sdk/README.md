# SDKs and Specialist Roles

Language SDKs and the built-in multi-agent orchestration roles.

## SDKs

Local-engine SDKs (the supported integration path):

- **Python** — [sdk/python/kern_sdk](sdk/python/kern_sdk) (stdlib-only; `KernEngine` + framework adapters)
- **TypeScript** — [`sdk/typescript/src`](sdk/typescript/src) (zero-dep; `KernEngine`)

> **Deprecated:** the REST `Client`/`callTool` classes targeted the retired
> `kern-server` control plane (`/v1/*`, including `POST /v1/tools/{name}`).
> They are kept as contract-pinned references (`sdk/contract/tools_call.json`)
> and will fail against a current install. For programmatic tool calls use MCP
> JSON-RPC over `kern mcp` (stdio) or `kern-mcp --http`.

---

## Multi-Agent Specialist Squad (The 7 Roles)

`kern` embeds a 7-role specialist squad architecture (`kern run` / `internal/agents`) that autonomous agents can orchestrate across the **Explore → Plan → Edit → Verify** engineering loop:

| Specialist Role | Focus Area | Autonomy Level | Key Trigger |
|---|---|---|---|
| **Planner** (`RolePlanner`) | Requirements analysis, milestone phasing & boundary scoping | L0–L3 | `kern_plan` / `kern_meta("plan <task>")` |
| **Architect** (`RoleArchitect`) | AST call graph verification, interface satisfaction & module boundaries | L0–L3 | `kern_explore` / `kern_meta("architecture...")` |
| **Coder** (`RoleCoder`) | Isolated `.kern/sandboxes/` edits with `TreeDiff` payload extraction | L2–L3 | `kern-safe-change` / `kern_meta("refactor")` |
| **Reviewer** (`RoleReviewer`) | AST diff auditing, code maintainability & anti-pattern detection | L0–L2 | `kern_review` / `kern_meta("review staged changes")` |
| **Security** (`RoleSecurity`) | Secret scans, dependency audit & policy firewall gates | L0–L2 | `kern_verify (security)` / `kern sec` |
| **Tester** (`RoleTester`) | Reproduction test fixture synthesis & test pass rate verification | L0–L2 | `kern_verify (test)` / `kern_heal` |
| **SRE** (`RoleSRE`) | AST symbol stack trace correlation, log compression & incident triage | L0–L4 | `kern-incident-triage` / `kern_optimize (log)` |

---
