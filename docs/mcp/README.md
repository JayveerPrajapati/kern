# Kern MCP Contracts

This directory documents kern's Model Context Protocol (MCP) server contracts — the
interface between MCP clients (agents, editors, CLIs) and the kern MCP server,
implemented in [`internal/mcp/`](../../internal/mcp/).

A **contract** is the server's promise to clients: the tools it exposes, their input
shapes, the wire protocol, and how both evolve.

## What kern's MCP server is

`kern-mcp` is a local, index-backed MCP server exposing kern's code-intelligence,
planning, execution, governance and security capabilities as MCP tools, so any
MCP-capable agent queries a codebase through kern's prebuilt symbol index instead of
re-reading files. All tools run locally; the only network call in kern is the explicit
`kern_doc` (action=fetch).

## Tool advertisement: the default surface vs KERN_MCP_FULL=1

The server practices progressive disclosure at the advertisement layer:

- **Default (no env):** the direct eleven-tool surface — `kern_buddy`, `kern_search`, `kern_explore`, `kern_context`, `kern_probe`, `kern_research`, `kern_impact`, `kern_graph`, `kern_review`, `kern_verify`, and `kern_meta`. This is what `kern setup`'s `.mcp.json` wires and a plain handshake's view.
- **`KERN_MCP_FULL=1`:** the full 34-tool catalog, returned by `tools/list` in a single unfiltered response (no `nextCursor`/`_meta` pagination). The retain/cursor mechanism pages tool *output* (retained `slice=` reads), not `tools/list` itself.

Direct `tools/call` requests are limited to the advertised surface: an unadvertised tool is rejected as `unknown tool` unless `KERN_MCP_ALLOW_UNADVERTISED=1` is set. `kern_meta` routes within that same advertised surface; `KERN_MCP_FULL=1` expands it to the full 34-tool catalog. Use `KERN_TOOLS` when you need a real execution allowlist for every advertised tool.

## Document index

| Document | Contents |
|---|---|
| [`tool-contracts.md`](tool-contracts.md) | Authoritative catalog of all 34 MCP tools: name, phase, risk, parameters, and JSON-RPC examples. |
| [`protocol.md`](protocol.md) | Wire protocol: transports (stdio / HTTP), JSON-RPC methods, governance gates, shutdown behavior. |
| [`versioning.md`](versioning.md) | Versioning policy: supported MCP protocol versions, catalog stability, client negotiation. |

## Catalog at a glance

- **34 tools** across six phases: explore, plan, edit, verify, meta, cross.
- **Risk levels** on every tool: low (21), medium (8), high (3), critical (1).
- Every tool is tagged with its agent **phase**; phase-aware servers advertise only the
  relevant shortlist plus the always-on meta/cross tools.

## Source of truth

The single source of truth for the tool catalog is the registration table in
[`internal/mcpcatalog/tools.go`](../../internal/mcpcatalog/tools.go) (`var All = []Tool{...}`).
[`internal/mcpcatalog/tools.go`](../../internal/mcpcatalog/tools.go) also defines the
`Tool` struct (`name`, `description`, `inputSchema`, `phase`, `riskLevel`) plus the
phase and risk constants; `internal/mcp/server.go` implements the JSON-RPC methods.
Catalog-parity invariants (plugin ↔ MCP, docs ↔ MCP) read the table via `ToolNames()`.

If this documentation and the code disagree, **the code wins** — file an issue so the
docs can be regenerated.

## How to use these docs

1. **New to kern MCP?** Read this README, then `tool-contracts.md` and `protocol.md`.
2. **Integrating a client?** `protocol.md` (handshake, transports, errors) and `versioning.md`.
3. **Looking up a tool?** `tool-contracts.md` — one section per tool.

## Related

- [`ARCHITECTURE.md`](../../ARCHITECTURE.md) — system architecture and package subsystem boundaries.
- [`internal/mcpguide/usage_guide.go`](../../internal/mcpguide/usage_guide.go) — the `kern guide`
  text: phase shortlists, performance tiers and pitfalls (served verbatim to the CLI
  and plugin).
