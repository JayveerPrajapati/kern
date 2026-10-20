# kern setup — global wiring, agent matrix, and alignment

`kern setup` wires the kern-first policy and the MCP server into every
supported agent. This page documents what the global surface covers, which
agents are supported, how presence is detected, and what happens when a new
agent is installed after kern.

## Scope of a run

`kern setup` has four scopes; combine them freely.

| Scope | Flag | What it writes |
|---|---|---|
| Project, detected only | *(default)* | `.mcp.json` (universal MCP config), `AGENTS.md` rules, and config/instruction files **only for agents detected on this system**. `opencode.json` + the opencode plugin when opencode is present. |
| Project, every agent | `--all-agents` | Same as default, but config surfaces for **all** supported agents, detected or not. |
| User-global | `--global` | Everything above **plus** user-level state: global `AGENTS.md` rules (marker-managed, personal content preserved), global opencode config + plugin (both global plugin locations, compare-and-write with backup), global Claude `CLAUDE.md`, home-scope MCP adapters (claude, codex, gemini, continue, kiro, qwen, qoder, cursor, copilot, antigravity, windsurf, zed), per-agent hooks, global git hooks + gitignore + editor exclusions, and global skills. |
| User-global only | `--global-only` | Only user-level state — **no file is written in any repo**. |
| Global rules block | `--global-rules` | The kern-rules block in each host's global instruction slot (`~/.claude/CLAUDE.md`, `~/.codex/AGENTS.md`, `~/.config/opencode/AGENTS.md`, `~/.cursor/rules/kern.mdc`, …), marker-delimited so personal content outside the markers is preserved. |

Every scope is idempotent and safe to re-run. `kern setup --dry-run` previews
exactly what a real run would write; `kern setup --check` prints the full
inventory; `kern doctor` reports wiring findings.

## Supported agents

kern auto-detects 13 agents. An agent counts as "detected" when any of its
markers exists in the project, its home config exists, or its binary is on
`PATH`:

| Agent | Detection markers |
|---|---|
| opencode | `opencode.json` · `opencode` on PATH |
| claude | `CLAUDE.md` · `.claude/settings.json` · `claude` on PATH |
| codex | `.codex/config.toml` · `codex` on PATH |
| cursor | `.cursor/mcp.json` · `.cursor/rules` · `cursor` on PATH |
| copilot | `.vscode/mcp.json` · `.github/copilot-instructions.md` |
| gemini | `GEMINI.md` · `.gemini/settings.json` |
| antigravity | `.agents` · `~/.gemini/antigravity-cli` · `~/.gemini/config/mcp_config.json` · `agy` on PATH |
| continue | `.continuerc.json` · `~/.config/continue/config.json` |
| windsurf | `.windsurfrc` · `.codeium/windsurf/mcp_config.json` · `~/.config/.codeium/windsurf/mcp_config.json` |
| zed | `~/.config/zed/settings.json` |
| qwen | `~/.qwen/settings.json` |
| qoder | `~/.qoder/settings.json` · `~/.qoder/mcp.json` |
| kiro | `.kiro/settings/mcp.json` · `~/.kiro/settings/mcp.json` |

`AGENTS.md` is written unconditionally (not just for detected agents): it is
the universal instruction file, read natively by opencode, Claude, Codex,
Gemini, Continue, Windsurf, Zed, Qwen, Qoder and Kiro, so the kern-first
policy reaches every agent regardless of wiring.

## Why absent agents can appear "wired"

Global and `--all-agents` runs **deliberately pre-wire every supported
agent**, detected or not: an agent installed later already finds kern's MCP
entry in its config, with no re-run. The trade-off is that config surfaces
exist for agents that are not installed yet — this is by design, and the
display says so:

- `kern setup --check` and setup run output annotate surfaces for
  undetected agents with **"(agent not installed on this system)"**.
- The check summary reports the honest companion count:
  `… · P/T agents detected` — P of the T known agents are actually present.

Detection is never a hard gate on *global* wiring; it only selects the
project-scope default.

## Installing a new agent after kern

Config surfaces are already in place thanks to pre-wiring, and `AGENTS.md`
carries the policy. Two optional steps keep the alignment complete:

1. Run `kern setup` once in each project (idempotent) so the new agent's
   per-agent instruction/rule files are written in the same run that detects
   it.
2. `kern doctor` detects and reports any present-but-unwired agent.

There is deliberately **no background watcher** that installs hooks when a
new agent appears — kern does not run daemons or scan the machine by
default. `kern setup` is the alignment step, and it is cheap.

## Memory boundary

Agent wiring, rules, hooks, skills and the opencode plugin can all be
user-global. **`kern memory` is intentionally per-project**: the project
brain (`kern_memory` add/recall) lives under each repo's `.kern` store, and
the global git hook writes captured memory into the repo it runs in. There
is no global memory store; cross-project context belongs in the global rules
or in each project's own brain.

## Removing kern

`kern setup --remove` reverses the wiring: per-agent MCP entries, hooks,
global rules blocks, plugin copies (customized copies are backed up before
removal) and skills. Foreign content is left untouched.