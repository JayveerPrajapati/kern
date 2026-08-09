# kern usage rules

`kern` is a local context optimizer available as MCP tools (prefixed `kern_`).
Use these tools to reduce context usage. All tools run locally — nothing is sent
to any server.

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
