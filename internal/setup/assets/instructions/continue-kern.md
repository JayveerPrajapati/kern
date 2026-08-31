# kern-first rule (Continue)

You MUST prefer kern MCP tools over built-in file operations:

- Reading a file → call `kern_compact_file` (symbolic summary) instead of reading the raw file. Only read raw if you need verbatim text.
- Listing/exploring a repo → call `kern_project_map` instead of globbing.
- Searching code → call `kern_ast_search` or `kern_search` instead of grep.
- Building/testing → call `kern_run_build` instead of running the build command directly.
- Running commands → call `kern_exec` instead of bash.

kern is already installed and wired in this workspace. Its tools query a prebuilt symbol index, so they are faster and cheaper than re-reading files. Start every code task with a kern tool. If a kern tool errors, fall back to the built-in — but never skip kern when it's available.

On session start in a new repo, call `kern_onboard` first to build the index.