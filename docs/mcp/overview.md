# MCP Overview

How to connect supported agents and how phase-aware tool routing, advertisement, and governance work.

## Connect to Your Agent

`kern setup` connects to 11 MCP clients automatically (12 config surfaces incl. Copilot's global MCP config). To configure manually:

<details open>
<summary><b>Claude Code</b></summary>

```bash
claude mcp add kern -- kern mcp
```
</details>

<details open>
<summary><b>Cursor / VS Code (.mcp.json)</b></summary>

Add to your project's `.mcp.json`:
```json
{
  "mcpServers": {
    "kern": { "command": "kern", "args": ["mcp"] }
  }
}
```
</details>

<details>
<summary><b>Codex, Gemini CLI, Windsurf, Zed, Continue</b></summary>

Run `kern setup` or check [`docs/mcp-client.md`](docs/mcp-client.md) for custom JSON adapter instructions.
</details>

---

## MCP Tools & Routing

By default, `kern` advertises a **direct eleven-tool surface**: `kern_buddy`, `kern_search`, `kern_explore`, `kern_context`, `kern_probe`, `kern_research`, `kern_impact`, `kern_graph`, `kern_review`, `kern_verify`, and the optional `kern_meta` router. `KERN_MCP_FULL=1` advertises the full 34-tool catalog directly; `kern_meta` routes within that same advertised surface with structured argument passthrough.

| Core Tool | Purpose | What it Replaces |
|---|---|---|
| **`kern_buddy`** *(default)* | Session onboarding digest: conventions, layout, entry points, gotchas | Fresh-repo spelunking |
| **`kern_search`** *(default)* | Sub-millisecond AST symbol search by name [measured]; ranked/prose ~17 ms | `grep` / `find` |
| **`kern_explore`** *(default)* | Full symbol call hierarchy, callers, callees, and blast radius | Manual file crawls |
| **`kern_context`** *(default)* | Minimal relevant source slice for a symbol (definition + callers + callees) | `cat` / `read` |
| **`kern_probe`** *(default)* | Task → budget-capped multi-symbol context bundle | Ad-hoc context gathering |
| **`kern_research`** *(default)* | Read-only multi-symbol research with impact and affected files | Manual search/context/impact loops |
| **`kern_impact`** *(default)* | Predicts blast radius, risk rating, and test gaps before changing code | Guesswork refactoring |
| **`kern_graph`** *(default)* | One-call graph context: callers, callees, blast radius | Manual call-graph walks |
| **`kern_review`** *(default)* | Token-optimized code review context for diffs and pull requests | Whole-file diff reviews |
| **`kern_verify`** *(default)* | Unified verification engine across build, test, security, and architecture | Fragmented check scripts |
| **`kern_meta`** *(default)* | Optional natural-language router for the advertised surface | Guessing tools |
| **`kern_apply`** | Transactional edit path with verification and rollback | Unsafe multi-file agent edits |
| **`kern_plan`** | Deterministic multi-file implementation plan | Ad-hoc edits |
| **`kern_run`** | Orchestrates complete multi-step tasks | Manual tool chains |
| **`kern_exec`** | Governed execution (`KERN_EXEC_POSTURE=ask` by default; `closed` restores strict fail-closed) | Ungated shell access |
| **`kern_compact_file`** | Compact symbolic file summary; `tier=full` for verbatim | Whole-file reads |
| **`kern_memory`** | Project brain memory | Lost cross-session lessons |
| **`kern_evidence`** | Signed-evidence toolkit | Unverifiable claims |
| **`kern_doc`** | Project docs: search / fetch / index | Web searches |
| **`kern_project_map`** | Compressed whole-project map | Glob + full reads |
| **`kern_ast_search`** | AST-level symbol search | Regex grep |
| **`kern_stats`** | Token savings and cost estimates | Guessing token spend |
| **`kern_why`** | Symbol rationale and dependency counts | Blind archaeology |
| **`kern_heal`** | Self-correction loop in a throwaway snapshot | Manual fix loops |
| **`kern_fit_context`** | Fit files/symbols into a token budget | Token-budget guessing |
| **`kern_plan_context`** | Deterministically plan context for a change | Over-/under-fetching |
| **`kern_path`** | Shortest call path between two symbols | Manual call-graph walks |
| **`kern_semcache`** | Inspect/manage the semantic cache | Repeated identical queries |
| **`kern_swap`** | Swap fenced code blocks ↔ symbolic signatures | Raw code dumps |
| **`kern_mask_pii`** | Local secret/PII scan | Prompt leaks |
| **`kern_fetch_raw_anchor`** | Re-read truncated output | Lost truncated output |
| **`kern_arch`** | Architecture overview and coupling warnings | Guesswork architecture |

*The eleven ***(default)*** tools are the direct advertised surface. `KERN_MCP_FULL=1` advertises all 34 tools directly, while `kern_meta` routes within the advertised surface behind its governance gates. `KERN_MCP_PHASE=explore|plan|edit|verify` filters either surface, and `KERN_MCP_SINGLE_TOOL=1` collapses to `kern_meta` alone.*

For the generated tool-selection guide, routing matrix, risk levels, and
when-to-use guidance, see [`docs/tool-selection.md`](docs/tool-selection.md).

---
