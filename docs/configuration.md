# kern Configuration

The full configuration catalog for kern. The README keeps a compact summary
and links here. kern is **zero-config by default** — nothing to write or keep
in sync to get started. Language support is automatic from file extensions;
there's nothing to wire per language. What exists:

- **`.kern/boundaries.json`** (optional) — architecture guardrails. Declare
  forbidden dependency crossings (e.g. a frontend importing a backend DB
  model); `kern guard` rejects a diff before it
  touches the filesystem. `kern guard init` writes a starter file.
- **Doc search index** (optional) — `kern docs index` (or
  `kern_doc action=index, semantic=true`) embeds project docs with a local Ollama
  model for real-meaning search (`kern_doc action=search`), stored in user cache.
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
  process detects the live owner and runs without its
  own relay. Deep workspace paths that exceed the OS socket-path limit
  transparently bind under a hashed name in the temp dir.
- **Environment variables** — `OLLAMA_HOST` (default `http://localhost:11434`,
  used only when you opt in), `KERN_LLM_PROVIDER` (`auto` default — the chain
  host sampler → local agent CLIs → Ollama, see [local-llm.md](local-llm.md);
  `openai`/`anthropic`/`google`/an agent-CLI name pin one provider;
  `none`/`off`/`disabled` disables LLM use entirely — offline and
  deterministic, no probe attempts), `KERN_LLM_REQUIRED` (default off;
  `1` makes a missing LLM provider a hard `kern doctor` failure instead of
  graceful degradation — CI/enterprise gating), `KERN_EMBED_MODEL` (default
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
project's console. `KERN_MCP_FULL=1` / `KERN_MCP_PHASE` / `KERN_MCP_CATEGORY` /
`KERN_MCP_SINGLE_TOOL` / `KERN_MCP_HIGH_LEVEL_ONLY` shape only what `tools/list`
**advertises** — a named `tools/call` always dispatches through the full catalog.
`KERN_MCP_FULL=1` advertises all 31 tools instead of the 29-tool default;
`KERN_MCP_PHASE=explore|plan|edit|verify` filters either tier down to the active
agent phase plus the always-on meta/cross tools; `KERN_MCP_CATEGORY=<family>`
keeps one tool family (e.g. `graph`, `verify`) with `kern_meta` always present;
`KERN_MCP_SINGLE_TOOL=1` collapses the advertised surface to `kern_meta` alone;
`KERN_MCP_HIGH_LEVEL_ONLY=1` is the legacy 6-tool middle set, deprecated in
favor of the default surface or `KERN_MCP_FULL=1`.
- **`.kern/config.json`** (optional) — one config path for operator knobs,
  resolved as **env var > `.kern/config.json` > built-in default**, per
  project root. JSON, stdlib-only, parsed once per root; a malformed file
  prints one warning and falls back to defaults. `kern config` prints every
  effective value and its source; `kern config --json` emits the same as JSON.

  ```json
  {
    "llm": { "provider": "auto", "model": "llama3.2",
             "required": false,
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
  `KERN_MCP_AUTH_TOKEN` (opt-in bearer token for the HTTP MCP transport: when
  set, `POST /mcp` requires `Authorization: Bearer <token>`; `/health` stays
  open; unset keeps the default owner-only unix-socket / loopback behavior),
  `KERN_GITHUB_TOKEN`, `KERN_K8S_TOKEN`, OpenAI/Anthropic/Gemini keys,
  `KERN_ALLOW_*`, `KERN_MCP_PERMISSIVE`, `KERN_MCP_NO_CONFINE`, `KERN_TOOLS`,
  `KERN_MCP_FULL/PHASE/SINGLE_TOOL/HIGH_LEVEL_ONLY/AUDIT_DIR`,
  `KERN_MCP_TLS_CERT`/`KERN_MCP_TLS_KEY` (optional TLS for the HTTP MCP transport),
`KERN_ALLOW_LOOPBACK_FETCH`, `KERN_INDEX_SERIAL`, `KERN_MCP_WATCH*`, sandbox
isolation knobs, and installer vars.

### Machine-wide install, guard and first-index knobs

When kern is wired machine-wide (`kern setup --global-only`), a repo needs no
setup: the first agent call indexes it. These switches control that behavior.

| Setting | Default | Meaning |
|---|---|---|
| `KERN_AUTO_WIRE=0` | wire | Installer only: download kern but do not wire any agent. |
| `KERN_INDEX_WAIT_SECONDS` | `25` | Longest one MCP tool call waits on an index build. Past it the call returns the stale index if one exists, else a retryable "still building" error; the build keeps running. `0` waits indefinitely. One-shot CLI commands always wait. |
| `KERN_ALLOW_BROAD_INDEX=1` | unset | Allow automatic first indexing of `$HOME`, a filesystem root or a documents folder (refused by default; `kern index <dir>` is always allowed). |
| `KERN_DISABLED=1` | unset | Switch the agent hook guard off for this process: built-in read/grep/bash are not redirected. Not audited; does not affect the git hooks (use `KERN_BYPASS`). |
| `.kern/off` (file) | absent | Same, for one repo. Delete the file to re-enable. |
| `.kern/indexing` (file) | managed by kern | Written while a full first index build runs; the hook guard lets built-ins through while it is fresh (under 15 minutes old, so a crashed build cannot leave the guard open). Covers the shell hook guard only: the opencode plugin calls the `kern` CLI directly (2-minute ceiling), so it never sees the MCP "still building" error; it pre-warms the index when it loads instead (see `KERN_PRELOAD`). |
| `KERN_PRELOAD=0` | warm | Do not pre-warm the index. The MCP server normally starts building it when an agent connects (`initialize`), and the opencode plugin runs a background `kern buddy` when it loads, so the first real tool call finds it ready. Both go through the guarded auto-index path and honor `KERN_DISABLED` and `.kern/off`. |
| `KERN_BYPASS=1` / `KERN_ENFORCE=0` | unset | Emergency override for hooks and git hooks; audited to `~/.kern/audit/bypass.jsonl` (`KERN_BYPASS_REASON` records why). |
| `KERN_PROTECTED_BRANCHES` | `main master` | Space-separated branches the global pre-push hook refuses to force-push or delete. |
| `KERN_BIN` | `~/.local/bin/kern` | Where the hook guard looks for kern when it is not on `PATH`; if none is found the guard fails open. |

`kern doctor` reports the state of all of these for the current repo.

### Org governance environment variables

Org mode is opt-in through `KERN_ORG_ROOT`. The org policy document at
`<root>/.kern/org-policy.json` overrides the default firewall policy set for
every project under the org. `kern policy set|get|apply` manages the policy,
and `kern doctor` reports its state. There is no per-tool MCP RBAC tool in
the current catalog; loopback clients use the default governed agent and the
`KERN_TOOLS` execution allowlist.

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