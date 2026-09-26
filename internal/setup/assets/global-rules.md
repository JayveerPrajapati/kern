<!-- kern:global-rules begin (managed by `kern setup --global-rules`; keep personal prefs outside the markers) -->
# kern usage rules for agents — READ FIRST

Applies when kern is available in this workspace: `kern_*` MCP tools are
wired, or the repo's AGENTS.md carries a kern-rules section. If no kern
tools are present, consider running `kern setup` (then restart the agent);
otherwise ignore this file.

kern is a local context engine. It runs locally (nothing leaves your
machine) and is FASTER and CHEAPER than the built-in read/grep/glob for any
non-trivial codebase because it queries a prebuilt symbol index instead of
re-reading files.

## The kern_meta tool (preferred entry point)

Instead of choosing among 140 individual `kern_*` tools, call the single
**`kern_meta`** tool with a natural-language request. Kern classifies the
request (deterministic keyword matching — no LLM, no network) and runs the
right tool internally. Examples:

- `kern_meta(request="how does dispatch work?")` → runs `kern_explore`
- `kern_meta(request="what breaks if I change dispatch?")` → runs `kern_impact`
- `kern_meta(request="find the NewServer function")` → runs `kern_search`

Set `KERN_MCP_FULL=1` for all 140 tools, `KERN_MCP_PHASE=explore|plan|edit|verify`
for phase subsets, or `KERN_MCP_SINGLE_TOOL=1` for only `kern_meta`.

## Kern-first policy (ENFORCED & AUTONOMOUS)

**Call a `kern_*` tool before any built-in `read`, `grep`, `glob`, or `bash`
for the tasks below.** On hosts with pre-tool hooks (Claude Code, Cursor,
Gemini, Copilot, Qwen, Qoder, Codex) the built-in call is BLOCKED with a
redirect; on opencode the built-in is transparently routed to kern.

1. Read a file → `kern_compact_file` (symbolic summary) first; `read` only
   for verbatim full text (rare).
2. List/explore a repo → `kern_project_map`, NOT `glob`.
3. Grep a pattern → `kern_ast_search` (code) / `kern_doc_search` (docs).
   On opencode the built-in `grep` IS kern's AST symbol search — patterns
   are symbol queries, not regex; for regex use `bash grep -rn` (or
   raw=true).
4. Understand a symbol (callers/callees) → `kern_code_graph` / `kern_explore`,
   NOT `read` + `grep`.
5. Build/test/lint → `kern_run_build`, NOT `bash`.
6. Run a command → `kern_exec`, NOT `bash`. On macOS `kern_exec` FAILS CLOSED
   by design (no unprivileged-userns isolation); the refusal names the
   override (`KERN_ALLOW_UNISOLATED=1` / `KERN_ALLOW_NET=1`) — expect it,
   don't retry blindly.
7. Search web/docs → `kern_doc_fetch` then `kern_doc_search`, NOT
   `webfetch`/`websearch`.
8. Unsure which tool → `kern_usage_guide` or `kern_buddy` BEFORE falling
   back.

| When you need to…         | CALL FIRST      | Replaces    |
|---------------------------|-----------------|-------------|
| Grep for a pattern (code) | `kern_search`   | `grep`      |
| Read a source slice       | `kern_context`  | `read`      |
| Understand a symbol       | `kern_explore`  | `read`      |
| Orchestrate a whole task   | `kern_run`     | manual chain |

## Session start: onboard before exploring (MANDATORY)

In a repo you have not worked in before — or whenever a repo may not be
indexed — call `kern_onboard` (or `kern_buddy`) FIRST, before
reading/grepping/globbing the tree. It registers the repo, builds/refreshes
the index, and returns a status report. **Prefer the index over
re-exploring.**

Extras: `kern_commitmsg` (deterministic commit messages),
`kern_optimize_prompt` (strip noise before processing), `kern_stats`
(token-savings report); 7-role specialist squad via the
`kern-team-orchestration` skill.
<!-- kern:global-rules end -->
