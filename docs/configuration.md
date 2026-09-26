# kern Configuration

The full configuration catalog for kern. The README keeps a compact summary
and links here. kern is **zero-config by default** — nothing to write or keep
in sync to get started. Language support is automatic from file extensions;
there's nothing to wire per language. What exists:

- **`.kern/boundaries.json`** (optional) — architecture guardrails. Declare
  forbidden dependency crossings (e.g. a frontend importing a backend DB
  model); `kern guard` (or `kern_guard_check`) rejects a diff before it
  touches the filesystem. `kern guard init` writes a starter file.
- **Doc search index** (optional) — `kern docs index` (or
  `kern_doc_index(semantic=true)`) embeds project docs with a local Ollama
  model for real-meaning `kern_doc_search`, stored in user cache.
- **`.kern/agents.json`** (optional) — custom agent wiring. Declare
  forked/private agents as JSON (`name`, config `path` with `~`/`$VAR`
  expansion, servers `key`, `entry` shape `stdio`|`cmd`, `scope`
  `global`|`repo`) and `kern setup` wires them exactly like the built-in
  adapters; a name clash with a builtin overrides it. The user-scope
  counterpart is `~/.config/kern/agents.json` (project file wins on
  clash). Like the rest of `.kern/`, it is gitignored by default —
  unignore it to share a team's agent list.
- **Event relay** — `kern events serve` owns `.kern/events.sock` and fans
  the system event bus out to any number of watchers across processes;
  `kern events watch [--kind policy.evaluated,...] [--json]` streams it,
  and `kern events emit <kind> [--subject S] [--payload k=v]` publishes
  from scripts. `kern lock`/`unlock` publish lock lifecycle events
  (acquired/contended/released) through the relay, so concurrent agents
  see contention in real time. Guard check outcomes publish the same way:
violations and the not-configured warning are appended to
`.kern/events.jsonl` for replay when no relay is running, and streamed
live when one is. The socket is local-user-only; a second
  server (e.g. kern-server) detects the live owner and runs without its
  own relay. Deep workspace paths that exceed the OS socket-path limit
  transparently bind under a hashed name in the temp dir.
- **Environment variables** — `OLLAMA_HOST` (default `http://localhost:11434`,
  used only when you opt in), `KERN_EMBED_MODEL` (default
  `nomic-embed-text`), `KERN_VERSION`/`KERN_CHANNEL`/`KERN_INSTALL_DIR`
  (installer: `KERN_VERSION` pins a release tag, `KERN_CHANNEL` selects the
  channel `latest` resolves to — `latest` (default), `stable` (newest
  3-component tag, 4-component hotfixes excluded), or a regex over tag
  names; an explicit `KERN_VERSION` pin always overrides the channel).
`KERN_TOKENIZER` selects the token counter used across all sizing and
savings numbers: `estimator` (default — stable, heuristic, offline),
`bpe` (self-trained byte-level BPE), or exact OpenAI encodings
`cl100k` / `o200k` (official rank tables embedded in the binary; GPT-4o
and o-series models map to o200k, GPT-3.5/4 to cl100k via `KERN_MODEL`).
The estimator stays the default so historical numbers remain comparable;
`go run ./evaluate/bench` gates the exact counters against reference
counts. `KERN_CACHE_ARCHIVE_DAYS` (default 7) and `KERN_CACHE_TTL_DAYS`
(default 30) drive the cache garbage collector: entries untouched past
the archive age are gzip-compressed in place, and past the TTL they are
evicted (`kern cache [root] [--dry-run]` runs it on demand; it also
runs automatically, at most once an hour). `KERN_MCP_AUDIT_DIR` overrides
where the MCP server persists its tool-call audit chain (default
`<project>/.kern/audit`). `KERN_MCP_ROOTS` (comma-separated directories;
the `mcp.roots` config key and the historical `KERN_ROOTS` alias also
apply) widens the tool workspace of the **single-root stdio MCP server**
(`kern-mcp`): every tool root/dir/path argument is confined to these
roots, falling back to the startup directory when unset. **Per-App tool
servers ignore it:** the root-aware servers that back each web-console
`/v1/tools/{name}` passthrough (single-project `kern serve` and every
enterprise-mode project) confine to their App root ONLY — a
`KERN_MCP_ROOTS` value naming another project's tree can never widen one
project's console.
- **`.kern/config.json`** (optional) — one config path for operator knobs,
  resolved as **env var > `.kern/config.json` > built-in default**, per
  project root. JSON, stdlib-only, parsed once per root; a malformed file
  prints one warning and falls back to defaults. `kern config` prints every
  effective value and its source; `kern config --json` emits the same as JSON.

  ```json
  {
    "llm": { "provider": "ollama", "model": "llama3.2",
             "model_roles": {"planner": "gpt-4o"} },
    "tokenizer": "estimator",
    "cost_per_token": 0.00001,
    "cache": { "archive_days": 7, "ttl_days": 30 },
    "runtime": { "poll_interval": "30s", "prometheus_url": "", "otel_url": "",
                 "k8s": { "api": "", "namespace": "" } },
    "deploy": { "command": "", "timeout": "5m" },
    "exec": { "risk": "MEDIUM" },
    "mcp": { "roots": [] },
    "webhooks": {},
    "enterprise": { "projects": {} }
  }
  ```

  Every key also honors its historical env var (`KERN_MODEL`, `KERN_EXEC_RISK`,
  `KERN_WEBHOOKS`, `KERN_ROOTS`, …), which always wins. **Secrets and safety
  toggles stay env-only** and are never read from the file: `KERN_AUTH_TOKEN`,
  `KERN_GITHUB_TOKEN`, `KERN_K8S_TOKEN`, OpenAI/Anthropic/Gemini keys,
  `KERN_ALLOW_*`, `KERN_MCP_PERMISSIVE`, `KERN_MCP_NO_CONFINE`, `KERN_TOOLS`,
  `KERN_MCP_FULL/PHASE/SINGLE_TOOL/HIGH_LEVEL_ONLY/AUDIT_DIR`,
  `KERN_MCP_TLS_CERT`/`KERN_MCP_TLS_KEY` (optional TLS for the HTTP MCP transport),
`KERN_ALLOW_LOOPBACK_FETCH`, `KERN_INDEX_SERIAL`, `KERN_MCP_WATCH*`, sandbox
isolation knobs, and installer vars.

### Agent RBAC environment variables

Role-based access control for MCP agents (`kern_agent_role_rbac`, enforced at
the tool-dispatch choke point) is controlled by two env-only opt-ins:

| Variable | Default | Meaning |
|---|---|---|
| `KERN_ALLOW_RBAC_ASSIGN` | unset | When `1`, allows the `assign` action of `kern_agent_role_rbac` (role assignment). Fails closed otherwise — an agent must never be able to assign itself a role. |
| `KERN_RBAC_DEFAULT_DENY` | unset | When `1`, agents with no explicitly assigned role — including the built-in `default` principal that every unauthenticated call resolves to — are mapped to the read-only `reviewer` role instead of the legacy permit-all loopback trust. Explicitly assigned roles are unaffected. |

### Org governance environment variables

Org mode (enterprise server / org-wide policy + approvals + RBAC) is strictly
opt-in and is gated on its RBAC pairing:

| Variable | Default | Meaning |
|---|---|---|
| `KERN_ORG_ROOT` | unset | The org root directory: enables org mode. Blast radius: the org policy document at `<root>/.kern/org-policy.json` overrides the default firewall policy set for every project under the org, org-wins RBAC (org role assignments at `<root>/.kern/org-rbac.json` beat per-project roles), and the org-wide approval store (`/org/approvals`, deploy-gate approvals) becomes authoritative. **Pairing requirement:** with `KERN_ORG_ROOT` set, `KERN_RBAC_DEFAULT_DENY=1` is REQUIRED — enterprise `New()` and `WithOrgRoot` refuse to start without it, because org-wins RBAC rests on a client-asserted `agent_id` (untrusted input) and unassigned principals would otherwise keep the legacy permit-all trust, defeating the org-governance story. |
| `KERN_ORG_ALLOW_WEAK_RBAC` | unset | **Unsafe escape hatch.** When `1`, org mode is allowed without `KERN_RBAC_DEFAULT_DENY=1` — unassigned agents keep the legacy permit-all posture under org-wins RBAC. Only for deployments that genuinely need weak org RBAC; documented-unsafe. |

> **Security note — `agent_id` is untrusted input.** Every RBAC decision keys
> off the `agent_id` a caller asserts (tool-call argument, raw REST body).
> It is NOT authenticated identity: an agent that can supply another agent's
> id inherits that agent's role. Org mode mitigates this only when
> `KERN_RBAC_DEFAULT_DENY=1` (unassigned ⇒ read-only), never by trusting the
> id. Treat `agent_id` as a label, not proof of identity.

Interplay: `KERN_RBAC_DEFAULT_DENY=1` auto-permits the `assign` action of
`kern_agent_role_rbac` — but only for principals that are already permitted
the tool. Unassigned/reviewer principals are denied `kern_agent_role_rbac` at
dispatch like any other write tool, so a stranded read-only principal can
never self-elevate: self-assignment stays impossible by design. Role bootstrap
is therefore an operator out-of-band action — hand-edit
`<root>/.kern/rbac.json` (owner-only, atomic write) or run `assign` as a
privileged principal; a restart, or the next assignment, brings the change
into effect. `KERN_ALLOW_RBAC_ASSIGN=1` keeps working standalone (assign
without default-deny). Assignments persist to `<root>/.kern/rbac.json` and
survive restarts.

- **`.kern/kern.yaml`** (optional) — per-project log compression profiles and adaptive truncation rules. Configure custom patterns, context line padding (`keep_lines_before`/`keep_lines_after`), or complete removal (`action: strip_completely`).

  ```yaml
  profiles:
    default:
      truncate_rules:
        - match: "console.log"
          action: strip_completely
    nodejs-backend:
      exclude_patterns: ["node_modules/**", ".env"]
      truncate_rules:
        - match: "ValidationError:"
          keep_lines_before: 2
          keep_lines_after: 5
        - match: "console.debug"
          action: strip_completely
  ```

## What the index skips

Dependency/build/cache directories (`node_modules`, `vendor`, `dist`,
`target`, `.venv`, …), anything in `.gitignore` (root and nested), generated
files (path conventions or a "Code generated" banner), and files over a size
budget — so the index is your code, not third-party noise.