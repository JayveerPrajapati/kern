# Agent Note: MCP Client for External Servers
Status: implemented

## Problem
Kern was an MCP server only — the silent pipeline could not reach external MCP tool servers (filesystem, GitHub, database) that other clients already use.

## Decision
- `internal/mcpclient`: stdlib-only MCP client with two transports — stdio (newline-delimited JSON-RPC, initialize handshake) and streamable-http (single stateless POST per request). Tools only; resources/prompts unsupported (dsh-mcp-client scope).
- Naming contract adopted verbatim from dsh: public names `mcp__<server>__<tool>`, normalized to `[A-Za-z0-9_-]`, max 64 chars, SHA-256 suffix on lossy normalization; raw name only on the wire.
- `kern mcp-client add|list|rm|call` CLI + config at `.kern/mcp-servers.json` (gitignored); no server enabled by default.
- `kern_mcp_call` MCP tool bridges a configured server's tool; accepts raw or public names.
- No build tag needed: the client is stdlib-only, so it ships always-built (unlike tree-sitter/sqlite).

## Consequence
- Given up: dynamic tool-catalog mutation (bridged tools are not registered as native kern_* tools — they are reached through kern_mcp_call); session-based streamable-http negotiation; MCP resources/prompts.
- Verified against a real in-process echo server (full stdio round-trip: initialize → tools/list → tools/call).
