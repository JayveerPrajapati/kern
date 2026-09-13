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
  `nomic-embed-text`), `KERN_VERSION`/`KERN_INSTALL_DIR` (installer).
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
`<project>/.kern/audit`).
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

## What the index skips

Dependency/build/cache directories (`node_modules`, `vendor`, `dist`,
`target`, `.venv`, …), anything in `.gitignore` (root and nested), generated
files (path conventions or a "Code generated" banner), and files over a size
budget — so the index is your code, not third-party noise.