// Package agentwire wires kern's agent-facing config surfaces: the
// project-level .mcp.json, the opencode project/global MCP config, the
// opencode plugin (project + global), the AGENTS.md usage rules (repo and
// global host instruction slots), and the bundled skills (project + global).
// All operations are idempotent: existing entries are left untouched.
//
// The package is a one-way dependency leaf of internal/setup: setup imports
// agentwire for the wiring steps that were extracted here, and agentwire
// never imports setup.
package agentwire
