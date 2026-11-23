# Installation and Quickstart

Everything needed to install kern, opt into agent wiring, index a repository, and run the first queries.

## Quickstart

> ### The 60-second path
>
> 1. Run the installer below.
> 2. Open a project and run `kern index .`.
> 3. Ask your agent: **“What breaks if I change `Server.dispatch`?”**
>
> Agent wiring is opt-in. The installer only downloads and verifies the binaries; pass `--wire` (PowerShell `-Wire`) or run `kern setup --global` when you want machine-wide integration. Use `kern setup --dry-run` first to preview every write.

### 1. Install

One command, prebuilt static binary (no runtime required):

```bash
# macOS / Linux
curl -fsSL https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.sh | sh

# Windows (PowerShell)
powershell -ExecutionPolicy Bypass -c "irm https://raw.githubusercontent.com/JayveerPrajapati/kern/main/install.ps1 | iex"
```

Prefer to look before you install? The default install is download-only:
`kern doctor` reports install and index health and
`kern setup --dry-run` previews exactly what wiring would be written —
both read-only, nothing changed.

The installer prints every step as it completes (platform detection → release
resolution → download → checksum verification → install → macOS Gatekeeper
re-sign → `kern version` check → a real `kern-mcp` MCP initialize handshake)
and aborts loudly on any failure — never a silent partial install. On macOS
the handshake probe turns the "Gatekeeper kills kern-mcp" failure mode into
a deterministic per-install verdict with the exact fix commands on failure.

Beyond the default install, the script manages the full lifecycle:

```sh
curl -fsSL .../install.sh | sh -s -- status     # installed version, path, platform
curl -fsSL .../install.sh | sh -s -- upgrade    # compare + swap if newer (with backup)
curl -fsSL .../install.sh | sh -s -- uninstall  # the full reversal (wiring + user data + binaries + PATH; --yes skips prompt)
```

`uninstall` is the full reversal: agent/git hooks, MCP, rules, skills and
editor wiring, the kern block in `~/AGENTS.md` (surgically excised, backup
kept), `~/.kern` (indexes, approvals, audit, memory, secrets), the machine-secret
directory (`os.UserConfigDir`/kern: approval signing keys),
`~/.cache/kern`, the binaries, and the PATH rc lines naming this install
prefix — another prefix's PATH lines always survive. `kern uninstall` also
offers the current project its own cleanup when it is kern-managed. Use
`uninstall --prefix-only` for a CI or scratch `KERN_INSTALL_DIR` install:
that removes only the binaries and this prefix's PATH lines, leaving all
machine state untouched. Pin a
version with `KERN_VERSION=v1.2.3`, relocate with `KERN_INSTALL_DIR=dir`;
when a platform has no prebuilt asset the installer falls back to
`go install` automatically. Windows users get the native PowerShell installer
(`install.ps1` — the same install/upgrade/status/uninstall lifecycle, with
checksum-verified downloads and the same full / prefix-only uninstall:
`-PrefixOnly` keeps it to binaries + User PATH; the default removes
`~/.config/kern` / `~/.cache/kern`, `~/.kern` and the kern block from
`~/AGENTS.md`); Git-Bash users can run `install.sh` (zip path documented).
`kern update` is uniform across platforms — it runs `install.sh` on
macOS/Linux and `install.ps1` via PowerShell on Windows automatically,
including replacing a running `kern.exe` (renamed aside).

> **Install once, then opt into wiring when you want it.** Machine-wide setup
> (`kern setup --global-only --global-rules`) installs user-level MCP
> registration, hooks, and rules for every supported agent, plus a global git
> ignore for `.kern/`. It writes no file in any repo. The first time an agent
> works in a repo, kern indexes it on demand (the global rules tell the agent
> to call `kern_buddy` first). Your home `~/AGENTS.md` is backed up to
> `~/AGENTS.md.bak.<timestamp>` before its kern block is rewritten.
>
> - **Look first, change nothing:** `kern doctor` verifies install health and
>   `kern setup --dry-run` previews the files wiring would write — both
>   read-only.
> - **Git hooks, machine-wide:** pre-commit runs
>   `kern verify --staged security,architecture`;
>   pre-push refuses force-push and deletion of `main`/`master` (change with
>   `KERN_PROTECTED_BRANCHES="main release"`); each repo's own hooks still run.
>   Both fail open if kern is missing. Emergency override, audited to
>   `~/.kern/audit/bypass.jsonl`: `KERN_BYPASS=1 KERN_BYPASS_REASON="..." git push`.
> - **Machine-wide wiring is opt-in:** pass `--wire` (PowerShell `-Wire`)
>   or set `KERN_AUTO_WIRE=1`, then run `kern setup --global-only` when
>   you choose. Preview with `--dry-run`.
> - **Undo:** `kern setup --remove` removes only kern's own entries and blocks
>   from your user-level config (preview with `--dry-run`, limit with
>   `--agents`; edited files are backed up first; `~/.kern` data is kept).
> - **Safety rails:** kern will not auto-index your home directory, a
>   filesystem root or a documents folder (override: `KERN_ALLOW_BROAD_INDEX=1`;
>   `kern index <dir>` is always allowed). The global hook **fails open**: if
>   kern is missing, or the repo has a `.kern/off` file, or `KERN_DISABLED=1`
>   is set, built-in read/grep/bash are not redirected.
> - **Share setup with a team:** run `kern setup` inside a repo to add the
>   shared config files (`.mcp.json`, `AGENTS.md`, plugin, skills) for the
>   agents it detects (`--all-agents` for every supported agent, `--plain` for
>   non-interactive output; interactive runs show a plan and ask first).
>   Skip `setup` entirely if you prefer to register the MCP server by hand —
>   e.g. `claude mcp add kern -- kern mcp` (see
>   [Connect to Your Agent](#connect-to-your-agent)).

**Release channels** — `KERN_CHANNEL` selects which release `latest`
resolves to (an explicit `KERN_VERSION=v1.2.3` pin always overrides it):

- `latest` (default) — the newest release, 4-component hotfixes included
  (today's `/releases/latest` behavior).
- `stable` — the newest 3-component release; 4-component hotfixes like
  `v0.9.9.1` are never picked automatically.
- any other value — a regex over release tag names, e.g.
  `KERN_CHANNEL='^v0\.9\.'` to stay on the 0.9 line (newest match wins).

The same selection is available from the CLI: `kern update --channel stable`
(forwarded to the installer as `KERN_CHANNEL`; `--pin <tag>` still wins).
A channel that resolves to an OLDER tag than the installed version is
refused by the same downgrade gate as today — only an explicit
`--pin <tag>` consents.

**Org governance** — `KERN_ORG_ROOT` turns on org mode (org-wide policy
distribution):

- `KERN_ORG_ROOT=/path/to/org-root` — the org root. Enables the org policy
  document (`<root>/.kern/org-policy.json`): org-wins policies that every
  project firewall under the org root derives its rules from. Managed with
  `kern policy set|get|apply` (see `docs/cli-reference.md`).

See `docs/configuration.md` → "Org governance environment variables" for the
full blast radius.

Prebuilt installs now **prefer the tree-sitter-enabled build**: the release
ships a `-full` asset per native platform (Linux amd64/arm64, macOS
amd64/arm64, Windows amd64), compiled with CGO on a matching runner, and
`install.sh` / `install.ps1` probe for it first. When no `-full` asset
exists (Windows arm64 has no native build yet, or the release is missing
one), the installer automatically falls back to the **pure-Go** build
(SQLite + regex-heuristic extraction — compiled with `-tags sqlite,notreesitter`),
which is what the cross-compiled tarballs have always shipped. Set
`KERN_FORCE_PUREGO=1` to skip the `-full` probe entirely. The default
**from-source** build (`make build`) also includes the tree-sitter indexer
and requires CGO (a C compiler). Opt out to the pure-Go build with
`make build-purego` / `make install-purego`, or set `KERN_PUREGO=1` when
the installer falls back to `go install`.

<details>
<summary><b>Other install options (Homebrew, Go install, Source)</b></summary>

```bash
# Homebrew
brew install --build-from-source ./homebrew/kern.rb

# Go Install (Go 1.27+; the default build includes tree-sitter and needs a
# C toolchain — add -tags notreesitter for the pure-Go build, or set
# KERN_PUREGO=1 when installing via install.sh/install.ps1)
go install github.com/JayveerPrajapati/kern/cmd/kern@latest
go install github.com/JayveerPrajapati/kern/cmd/kern-mcp@latest

# Build from source (default = tree-sitter + CGO; make build-purego for pure-Go)
make build  # Produces bin/kern, bin/kern-mcp
```
</details>

### 2. Connect to Your Agent

Auto-configure Antigravity, Claude Code, Cursor, OpenCode, Gemini CLI, Codex, VS Code, Windsurf, Copilot, and all MCP clients:

```bash
kern setup           # Project-level MCP config & usage rules
kern setup --global  # Machine-wide: wires all agents + global git commit protection
```

### 3. Initialize & Index

```bash
cd your-project
kern index .        # Fast one-shot AST indexing
```

### 4. Ask Your Agent Anything

Ask your connected AI agent naturally:
> *"What breaks if I change `Server.dispatch`? Who depends on it, and why?"*

`kern` answers from the prebuilt index instantly — no file-by-file scanning.

### 5. Common Tasks

Everyday questions, answered by the CLI directly (agents reach the same
functions through the MCP tools):

| Task | Command |
|---|---|
| **How do I optimize a prompt?** | `kern optimize "…"` — strips filler, masks secrets, preserves context |
| **How do I compress a log?** | `kern optimize --kind log "…"` (or `kern compact`) — keeps errors + stack frames |
| **How do I search this codebase?** | `kern search <symbol>` — typed results, not raw grep lines |
| **What depends on this symbol?** | `kern impact <symbol>` — transitive blast radius + risk |
| **How do I review changes?** | `kern review` — token-optimized diff review against the index |
| **How do I check everything works?** | `kern doctor` — binary, agent configs, and index health |
| **How do I verify a change without the full suite?** | `kern verify --command "go test ./internal/foo/..." --output summary` — runs only what you name (a full `kern verify build,test` on a repo this size takes ~10 min [illustrative]; scope it to the packages you touched) |

Agents get the same answers via `kern_search`, `kern_optimize` (action=prompt|log),
`kern_impact`, `kern_review`, and `kern doctor`.
