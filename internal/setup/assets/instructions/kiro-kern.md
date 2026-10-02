# kern-first rule (Kiro)

You MUST prefer kern MCP tools over built-in file operations:

- Not sure which tool fits? Ask `kern_meta` — it routes natural language to the right kern tool.
- Reading a file → call `kern_compact_file` (symbolic summary) instead of reading the raw file. Only read raw if you need verbatim text.
- Listing/exploring a repo → call `kern_project_map` instead of globbing.
- Searching code → call `kern_search` instead of grep.
- Understanding a symbol (callers/callees) → call `kern_explore`.
- Estimating a change's blast radius → call `kern_impact`.
- Building/testing/verifying → call `kern_verify` instead of running the build command directly.

kern is already installed and wired in this workspace. Its tools query a prebuilt symbol index, so they are faster and cheaper than re-reading files. Start every code task with a kern tool. If a kern tool errors, fall back to the built-in — but never skip kern when it's available.

On session start in a new repo, call `kern_buddy` first for the onboarding digest.