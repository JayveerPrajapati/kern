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

## When a build, test, or long-running command is needed

Prefer `kern_run_build` over running the command directly and pasting full
output into context.

## Prompt hygiene

If a user prompt or attached data is large or noisy, optimize it first with
`kern_optimize_prompt` before processing.

## Savings tracking

Report token savings when asked: use `kern_stats`. This shows before/after
tokens and estimated cost saved.
