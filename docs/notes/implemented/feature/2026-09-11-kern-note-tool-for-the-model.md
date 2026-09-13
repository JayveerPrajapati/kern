# Agent Note: kern note Tool for the Model
Status: implemented

## Problem
The note:missing gate (G38) requires decision records for non-trivial changes, but the model could only create them through the CLI, not the tool surface it uses for everything else.

## Decision
- `kern_note` MCP tool: actions new (gate-conformant skeleton), status (lifecycle transition), validate (G37 report), list (inventory).
- The lifecycle transition was extracted from the CLI into `internal/note.Move` so both surfaces share one implementation.
- Plugin mirror synced to all 4 locations; catalog 127 tools.

## Consequence
- Agents working in kern's repo can satisfy G38 through MCP; CLI and tool behavior can never diverge (single Move implementation).
