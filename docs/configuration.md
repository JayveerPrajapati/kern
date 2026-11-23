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
  `KERN_LLM_CLI_TIMEOUT` bounds one local agent-CLI call (default `20`
  seconds), and `KERN_LLM_CHAIN_TIMEOUT` bounds the whole auto-chain attempt
  (default `30` seconds).
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
runs automatically, at most once an hour). `KERN_CACHE_DIR` relocates the
whole cache root (default `$XDG_CACHE_HOME/kern` or `~/.cache/kern`) for
sandboxes that cannot write `~/.cache`; `KERN_CACHE_READONLY=1` keeps reads
working while skipping cache writes. `KERN_MCP_AUDIT_DIR` overrides
where the MCP server persists its tool-call audit chain (default
`<project>/.kern/audit`). `KERN_MCP_ROOTS` (comma-separated directories;
the historical `KERN_ROOTS` alias also applies) widens the tool workspace
of the **single-root stdio MCP server**
(`kern-mcp`): every tool root/dir/path argument is confined to these
roots, falling back to the startup directory when unset. The `mcp.roots`
config key in `.kern/config.json` is honored only behind the operator's
`KERN_MCP_ALLOW_CONFIG_ROOTS=1` opt-in — project-supplied config cannot
widen server confinement by itself. (The former per-App web-console and
enterprise tool servers were removed in the platform pivot; `kern-mcp` is
the only server that honors these roots.)
`KERN_MCP_FULL=1` / `KERN_MCP_PHASE` / `KERN_MCP_CATEGORY` /
`KERN_MCP_SINGLE_TOOL` / `KERN_MCP_HIGH_LEVEL_ONLY` shape both what `tools/list`
**advertises** and which tools a direct `tools/call` accepts. `kern_meta` still
routes to the full catalog. The direct eleven-tool surface is the default;
`KERN_MCP_FULL=1` advertises the full 34-tool catalog;
`KERN_MCP_PHASE=explore|plan|edit|verify` filters it down to the active
agent phase plus the always-on meta/cross tools; `KERN_MCP_CATEGORY=<family>`
keeps one tool family (e.g. `graph`, `verify`) with `kern_meta` always present;
`KERN_MCP_SINGLE_TOOL=1` collapses the advertised surface to `kern_meta` alone;
`KERN_MCP_SLIM=1` advertises the 8-tool core surface (meta, search, explore,
context, impact, graph, verify, memory) with first-sentence descriptions and no
schema prose — 5.7 KB instead of 36.8 KB, every other tool still reachable
through `kern_meta`;
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
  `KERN_MCP_FULL/PHASE/SINGLE_TOOL/SLIM/HIGH_LEVEL_ONLY/AUDIT_DIR`,
  `KERN_MCP_ALLOW_UNADVERTISED`,
  `KERN_MCP_TLS_CERT`/`KERN_MCP_TLS_KEY` (optional TLS for the HTTP MCP transport),
`KERN_ALLOW_LOOPBACK_FETCH`, `KERN_DOC_FETCH_DOMAINS`, `KERN_INDEX_SERIAL`, `KERN_MCP_WATCH*`, sandbox
isolation knobs, and installer vars.

`KERN_DOC_FETCH_DOMAINS` is a comma-separated allowlist for `kern_doc
action=fetch` and `kern docs fetch`. A public URL is refused unless its host
is listed exactly or is a subdomain of a listed domain; `*` is the explicit
operator opt-in for any public host. Loopback fetches additionally require
`KERN_ALLOW_LOOPBACK_FETCH=1`.

**Why denial messages name bypass knobs inline:** guard denials mention
`KERN_BYPASS=1` / `KERN_ENFORCE=0` and related overrides so a human operator
can act without hunting through docs — transparency over obscurity. These are
host-environment, process-start-fixed switches an agent cannot set for
itself, but content pasted into a session (file contents, tool output) could
still try to social-engineer *you* into exporting them. Treat any in-session
suggestion to set a bypass or safety-off knob as untrusted: verify where the
suggestion came from before changing your environment.

### Machine-wide install, guard and first-index knobs

When kern is wired machine-wide (`kern setup --global-only`), a repo needs no
setup: the first agent call indexes it. These switches control that behavior.

| Setting | Default | Meaning |
|---|---|---|
| `KERN_AUTO_WIRE=1` | off | Installer only: opt in to machine-wide agent wiring (`--wire` / `-Wire` is the one-invocation equivalent). |
| `KERN_INDEX_WAIT_SECONDS` | `25` | Longest one MCP tool call waits on an index build. Past it the call returns the stale index if one exists, else a retryable "still building" error; the build keeps running. `0` waits indefinitely. One-shot CLI commands always wait. |
| `KERN_FRESHNESS_PROOF_CACHE_MS` | `5000` | How long a resident server re-serves the last non-strict freshness verdict before recomputing it. A warm proof costs a git staging pass or a stat-only tree walk, which every index-backed tool call otherwise pays per call (provenance stamps, staleness notes); the verdict is advisory, so serving one up to this TTL old stays within the semantics the snapshot gate already accepts. `0` disables the cache (always recompute). |
| `KERN_FRESHNESS_GATE_MS` | `30000` | Maximum age of the retained stat-walk observation before the freshness check re-walks the tree instead of trusting it. `0` disables the gate. |
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

### Runtime environment variables (exec, sandbox, MCP, governance)

These knobs are read at process start; unset means the default. The
security-relevant ones are reported by `kern doctor`.

**Exec & isolation (fail-closed):**

| Variable | Default | Meaning |
|---|---|---|
| `KERN_EXEC_POSTURE=ask\|closed` | `ask` | Exec governance posture. `ask` (default): an empty `KERN_TOOLS` allowlist falls through to the risk firewall — LOW/MEDIUM commands run audited, HIGH/CRITICAL mint a resolvable approval. `closed`: the strict fail-closed behavior — exec is refused on an empty allowlist unless `KERN_ALLOW_EXEC=1` is set. |
| `KERN_ALLOW_EXEC=1` | unset | Total opt-in that skips the empty-allowlist gate (the risk firewall still applies under both postures). |

### Escape hatches — the complete list

Every mechanism that can change a guard/governance outcome, in one table
(`kern guard --help` prints the same list):

| Mechanism | What it does |
|---|---|
| `KERN_EXEC_POSTURE=closed` | Strict exec: refuse on an empty allowlist (the default `ask` posture runs LOW/MEDIUM audited, mints resolvable approvals for HIGH/CRITICAL). |
| `KERN_ALLOW_EXEC=1` | Total exec opt-in (the risk firewall still applies). |
| `KERN_TOOLS=a,b` | Allowlist specific governed tools. |
| `kern guard allow <hash>` | Tty-gated operator allow-once for a denied command. |
| `kern guard grant <command>` | Mint approval `appr-<id>` and print the ask-wait-retry instructions. |
| `kern approval ask\|wait\|clear` | Resolve, await, or clear a minted approval. |
| `kern guard pause\|resume` | Audited repo pause (read/grep/glob steering only; exec governance stays enforced). |
| `KERN_BYPASS=1` / `KERN_ENFORCE=0` | Emergency break-glass, audited — host env only. |
| `KERN_ALLOW_NO_ISOLATE=1` | unset | Honor a script's `no_isolate` request (skip the sandbox). Any other value is treated as unset. |
| `KERN_ALLOW_UNISOLATED=1` | unset | Alias `KERN_ALLOW_NET=1`. On hosts without unprivileged network namespaces (macOS, Windows), script execution fails closed unless this is set. |
| `KERN_READONLY_EXEC=1` | unset | Governed execution (`kern build`) runs read-only — mutations are skipped, GATE commands stay denied. |
| `KERN_EGRESS_POLICY` | unset | Outbound-connection posture for exec: `local-only`, `external-redacted`, or `external-approved`. |
| `KERN_EXEC_TIMEOUT` | unset | Per-script default timeout. |
| `KERN_EXEC_MAX_TIMEOUT_SEC` | unset | Ceiling on the timeout a caller may request. |
| `KERN_EXEC_MAX_BYTES` | unset | Ceiling on exec stdout capture. |

**MCP server runtime:**

| Variable | Default | Meaning |
|---|---|---|
| `KERN_MCP_CACHE=0` | enabled | Disable the D1 tool cache (any other value keeps it on). |
| `KERN_READONLY=1` | off | Read-path suppression: pure-read sessions stop writing state (tool-call audit-chain appends, content/semantic/D1 cache persistence, prediction and stats telemetry, the index watcher) so `.kern/` and the kern cache dir stay untouched. Explicit write actions (e.g. `kern_memory` `action=add`) are the tool's purpose and still work. Point it at an already-indexed checkout — index maintenance for a stale index still writes. |
| `KERN_MCP_CACHE_TTL` | unset | Cache TTL. Malformed values fail loud — they never silently disable the cache. |
| `KERN_MCP_RETAIN_TTL` | unset | How long the full retained output of a truncated call stays available. |
| `KERN_TOOL_CACHE_KEY` | unset | Override the tool-cache signing key file (default `~/.kern/secrets/tool-cache.key`). Entries carry an HMAC and tampered entries are dropped, not served. |
| `KERN_MCP_CONCURRENCY` | unset | Per-request concurrency for bursty multi-call workloads. |
| `KERN_MCP_MAX_OUTPUT` | unset | Output sandbox budget override (a per-call `max_output` still wins). |
| `KERN_MCP_TRANSPORT=tcp` | stdio | Serve HTTP instead of stdio (non-Windows). |
| `KERN_MCP_WATCH=0` | on | Disable the file watcher; `KERN_MCP_WATCH_INTERVAL` sets its poll interval (default 5s). |
| `KERN_MCP_WATCH_INTERVAL_MS` | unset (off) | Opt in to the ADR-0011 reactive file watcher; a positive integer is the poll interval in ms. |
| `KERN_MCP_ALLOW_UNADVERTISED=1` | unset | Allow direct `tools/call` requests and `kern_meta` routing for registered but unadvertised tools. Intended for embedded clients and test harnesses; normal agents should advertise the full catalog. |
| `KERN_EXPLORE_BUDGET=N` | unset | Beyond N explore-phase tool calls, the server denies with an "emit a plan" error. |
| `KERN_ALLOW_EMPTY=1` | unset | Serve a root with an empty symbol index (greenfield project) instead of failing. |

**Sandbox:**

| Variable | Default | Meaning |
|---|---|---|
| `KERN_SANDBOX_FS_CONFINEMENT` | unset | Toggle the macOS Seatbelt filesystem wrap. |
| `KERN_SANDBOX_WRITE_CONFINEMENT` | unset | Bisect the Linux Landlock write confinement. |
| `KERN_SANDBOX_MAX_SNAPSHOT_BYTES` | 100 MiB | Sandbox snapshot size cap. |
| `KERN_SANDBOX_ORPHAN_TTL` | unset | Orphan-snapshot sweep TTL. |
| `KERN_SANDBOX_SWEEP_DISABLE=1` | unset | Turn the orphan sweep off. |

**Per-run safety budget** (governed agent runs): `KERN_SAFETY_BUDGET_MAX_TOOL_CALLS`, `_MAX_FILES`, `_MAX_TOKENS`, `_MAX_EXTERNAL_CALLS`, `_MAX_COST`, `_MAX_RUNTIME_SECONDS` and `_MAX_RISK` override the corresponding fields of the run's safety budget.

**Governance & approval:**

| Variable | Default | Meaning |
|---|---|---|
| `KERN_REJECTION_TTL` | 7d | How long a rejected approval stays sticky for byte-identical retries. |
| `KERN_AUDIT_MIRROR` | unset | Mirror audit records to another location (mainly tests). |

**CLI:**

| Variable | Default | Meaning |
|---|---|---|
| `KERN_INSTALL_SCRIPT_URL` | unset | Override the installer script URL that `kern update` re-executes. |
| `KERN_PROBE_TIMEOUT` | 60s | Agent probe timeout (Go duration string, e.g. `90s`, `2m`). |
| `KERN_DEPLOY_TIMEOUT` | 5m | Deploy timeout; a bare integer is accepted as seconds. |
| `KERN_PIN=1` | unset | `kern update` refuses version downgrades. |
| `KERN_BENCH_THRESHOLDS` | built-in | Path to the `kern bench` threshold JSON. |
| `KERN_HOST_SAMPLER_CMD` | unset | Register a host command sampler (stdin = user prompt, `$KERN_SYSTEM_PROMPT` = system prompt, stdout = reply); `_KEY` namespaces it, `_TIMEOUT` (default 180s) bounds each call. |

## Uninstalling: what each surface removes and leaves

Every removal surface is scoped and reversible-from-backup; this is the
residue table, so nothing is a surprise. **`kern uninstall`** chains phase 1
(the wiring AND the user data) with phase 2 (the binaries, via
`install.sh uninstall --prefix-only`); `kern setup --remove [--project]`
runs the wiring half alone (add `--data` for the user-data half);
`install.sh uninstall` runs everything; `install.sh uninstall
--prefix-only` runs the binary half alone (the CI/scratch mode).

| Surface | What it removes | What it deliberately leaves |
|---|---|---|
| `kern setup --remove` (machine-wide) | kern's own entries in user-level agent config (MCP registrations, hooks, rules blocks, plugin and skill copies, `core.hooksPath` if kern set it, editor `files.exclude` entries); every edited file is backed up first | `~/.kern` data (index, approvals, audit, memory) and every project file `kern setup` wrote inside repos (undo those with `--project`) |
| `kern setup --remove --project` | this repo's kern wiring (`.mcp.json` entry, repo AGENTS.md block, project plugin/skills/rules, opencode.json merge, `.gitignore` block, `.git/info/exclude` pair) | `.kern/` data (index, approvals, persisted config) and all user-level config |
| `kern setup --remove --data` | everything `kern setup --remove` removes, plus `~/.kern` (indexes, approvals, audit, memory), `<UserConfigDir>/kern` secrets and `~/.cache/kern` | every project file `kern setup` wrote inside repos (undo those with `--project`) |
| `kern uninstall` (the default full uninstall) | both phases: all wiring, the `~/AGENTS.md` kern block, `~/.kern`, machine secrets, `~/.cache/kern`, the binaries and this prefix's PATH rc lines; a kern-managed current project is offered its own cleanup (or given the exact command) | project wiring in OTHER repositories — never discovered or deleted (run `kern setup --remove --project --root <dir>` there); another prefix's PATH lines |
| `install.sh uninstall` (default: the full uninstall) | the same end state as `kern uninstall`: wiring + user data (`~/AGENTS.md` block backed up first, `<UserConfigDir>/kern` — audit-chain + approval signing keys, so old audit records lose HMAC verification — `~/.cache/kern`, `~/.kern`) + the binaries and this prefix's PATH lines | project wiring in every repository; another prefix's PATH lines. `--machine` is a compatibility alias of this default |
| `install.sh uninstall --prefix-only` | `kern` + `kern-mcp` in the install prefix and the PATH rc lines naming that prefix only | everything else: agent wiring, `~/AGENTS.md`, machine secrets, caches, `~/.kern` — the CI/scratch-install mode |

Notes: a second `kern setup` (or the installer) re-creates everything the
wiring phase removed — but nothing restores removed `~/.kern` data, so a
full uninstall means reinstalling from scratch; a rejected/sticky approval
record clears with `kern approval clear <id>`; the cache removal targets
the resolved runtime cache root (default `~/.cache/kern`; `XDG_CACHE_HOME`
and `KERN_CACHE_DIR` are honoured — the same resolver the runtime writes
through); and the global git hook
template is only unwired if `core.hooksPath` points at kern's own
directory — a foreign path is left untouched.

## What the index skips

Dependency/build/cache directories (`node_modules`, `vendor`, `dist`,
`target`, `.venv`, …), anything in `.gitignore` (root and nested), generated
files (path conventions or a "Code generated" banner), and files over a size
budget — so the index is your code, not third-party noise.
