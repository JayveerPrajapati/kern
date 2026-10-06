# kern-first rule (Kiro)

You MUST prefer kern MCP tools over built-in file operations:

- Not sure which tool fits? Ask `kern_meta` — it routes natural language to the right kern tool.
- Reading a file → call `kern_explore` with the file path as symbol (symbolic summary; tier=full for verbatim text, start_line/end_line for a line window) instead of reading the raw file; kern_compact_file does the same when KERN_MCP_FULL=1.
- Listing/exploring a repo → call `kern_project_map` instead of globbing.
- Searching code → call `kern_search` instead of grep.
- Understanding a symbol (callers/callees) → call `kern_explore`.
- Estimating a change's blast radius → call `kern_impact`.
- Building/testing/verifying → call `kern_verify` instead of running the build command directly. Pass your own command to get a compact result: `kern_verify command="go test ./..." output=summary` (output: summary|failures|tail:N|lines:A-B|full); re-slice the kept run with `kern_verify anchor=<id> output=lines:A-B`.

kern is already installed and wired in this workspace. Its tools query a prebuilt symbol index, so they are faster and cheaper than re-reading files. Start every code task with a kern tool. If a kern tool errors, fall back to the built-in — but never skip kern when it's available.

On session start in a new repo, call `kern_buddy` first for the onboarding digest.