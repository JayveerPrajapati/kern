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

- **Default (no env):** a curated lifecycle surface of 6 tools — `kern_meta`
  (the natural-language router) plus `explore`, `impact`, `search`,
  `verify`, and `buddy`. This is what `kern setup`'s generated `.mcp.json`
  wires, and what a client sees from a plain handshake. The set is
  evidence-based (usage telemetry + phase coverage).
- **`KERN_MCP_FULL=1`:** the full 117-tool catalog, paginated per the MCP spec
  (clients must follow `nextCursor`; a naive single-page read sees only the first
  page — a known trap for hand-rolled clients).

Every tool is reachable in BOTH modes once named in `tools/call` — the modes differ
only in what is ADVERTISED. `kern_meta` routes natural language to any of the 117
tools, so the default surface loses no capability, only up-front schema cost (~14k
tokens, well under the 24.1k gate).

## Document index

| Document | Contents |
|---|---|
| [`tool-contracts.md`](tool-contracts.md) | Authoritative catalog of all 117 MCP tools: name, phase, risk, parameters, and JSON-RPC examples. |
| [`protocol.md`](protocol.md) | Wire protocol: transports (stdio / HTTP), JSON-RPC methods, governance gates, shutdown behavior. |
| [`versioning.md`](versioning.md) | Versioning policy: supported MCP protocol versions, catalog stability, client negotiation. |

## Catalog at a glance

- **117 tools** across six phases: explore, plan, edit, verify, meta, cross.
- **Risk levels** on every tool: low (70), medium (25), high (18), critical (4) —
  governed clients gate tool access on these.
- Every tool is tagged with its agent **phase**; phase-aware servers advertise only the
  relevant shortlist plus the always-on meta/cross tools.

## Source of truth

The single source of truth for the tool catalog is the registration table in
[`internal/mcp/catalog/tools.go`](../../internal/mcp/catalog/tools.go) (`var All = []Tool{...}`).
`internal/mcp/server.go` defines the `Tool` struct (`name`, `description`, `inputSchema`,
`phase`, `riskLevel`), the phase and risk constants, and the JSON-RPC method handling.
Catalog-parity invariants (plugin ↔ MCP, docs ↔ MCP) read the table via `ToolNames()`.

If this documentation and the code disagree, **the code wins** — file an issue so the
docs can be regenerated.

## How to use these docs

1. **New to kern MCP?** Read this README, then `tool-contracts.md` and `protocol.md`.
2. **Integrating a client?** `protocol.md` (handshake, transports, errors) and `versioning.md`.
3. **Looking up a tool?** `tool-contracts.md` — one section per tool.

## Related

- [`docs/authorized-context.md`](../authorized-context.md) — governed-mode context
  authorization (`kern_authorize_context`).
- [`ARCHITECTURE.md`](../../ARCHITECTURE.md) — system architecture and package subsystem boundaries.
- [`internal/mcp/usage_guide.go`](../../internal/mcp/usage_guide.go) — the `kern guide`
  text: phase shortlists, performance tiers and pitfalls (served verbatim to the CLI
  and the opencode plugin).