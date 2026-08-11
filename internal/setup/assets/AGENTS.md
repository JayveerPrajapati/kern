# kern usage rules

`kern` is a local context optimizer available as MCP tools (prefixed `kern_`).
Use these tools to reduce context usage. All tools run locally — nothing is sent
to any server.

## Kern-first policy (MANDATORY)

Any agent using this workspace MUST call a `kern_*` tool FIRST before using any
other MCP tool or built-in tool (read, grep, glob, bash, webfetch, etc.).
If multiple MCPs are configured, kern tools always take priority.

Concrete kern-first rules (replace the corresponding generic tool):

| When you need to…            | CALL THIS FIRST             | Replaces      |
|------------------------------|-----------------------------|---------------|
| Read a file                  | `kern_compact_file`         | `read`        |
| List / explore a repo        | `kern_project_map`          | `glob`/`glob` |
| Grep for a pattern           | `kern_doc_search` / `kern_ast_search` | `grep`  |
| Read a source slice          | `kern_context`              | `read`        |
| Understand a symbol          | `kern_code_graph` / `kern_explore` | `read`  |
| Search the web / docs        | `kern_doc_fetch` then `kern_doc_search` | `webfetch`/`websearch` |
| Build / test / lint          | `kern_run_build`            | `bash`        |
| Run a command / script       | `kern_exec`                 | `bash`        |
| Commit                       | `kern_commitmsg` or `kern commit` | git CLI  |
| Search (task/bug/error)      | `kern_probe`                | grep + read   |

**Heuristic: any task involving files, code, logs, builds, or web content
should start with a kern tool.** If the appropriate kern tool is unavailable
or not registered, fall back to built-ins — but never the reverse.

## Always prefer kern tools over raw reads

- Before reading a large file, use `kern_compact_file` to get its symbols.
- Before listing/reading a whole repo, use `kern_project_map`.
- Before pasting logs into context, use `kern_optimize_log`.
- To understand a symbol's role, use `kern_code_graph` (callers + callees).
- To get only the relevant slice of a source file, use `kern_context`.
- To search code by structure (not regex), use `kern_ast_search`.
- For builds/tests/lint, use `kern_run_build` — it returns only pass/fail + errors.
- To look up a library/framework's docs, use `kern_doc_fetch` to pull one public
  page into the project's local doc index, then `kern_doc_search` to query it.
  Pass `semantic=true` to also attach local Ollama embeddings to the page.

## When a build, test, or long-running command is needed

Prefer `kern_run_build` over running the command directly and pasting full
output into context.

## Git workflows

- Before committing, use `kern_commitmsg` to get a deterministic conventional
  commit message (type/scope/subject + per-file body) from the diff; it is
  rule-based and offline, so edit the result freely. `kern commit` can stage
  and commit in one step (CLI only — a machine committing on its own is too
  destructive to expose as a tool).
- `kern pack` and `kern project map` honor a root `.gitignore` and `.kernignore`
  (`.kernignore` wins), and packed bundles carry a `SECURITY` section of
  secrets/injection findings for files being sent to an agent.

## Prompt hygiene

If a user prompt or attached data is large or noisy, optimize it first with
`kern_optimize_prompt` before processing. Optimization results are cached, so
a repeated or reworded query (semantic cache) returns instantly with a
`served from ... cache` marker. Use `kern_semcache` to inspect or clear the
cache, or to preview whether two inputs are similar enough to hit.

## Savings tracking

Report token savings when asked: use `kern_stats`. This shows before/after
tokens and estimated cost saved.
