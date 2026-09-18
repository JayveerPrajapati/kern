# Kern MCP Contracts

This directory documents the stable contracts of kern's Model Context Protocol (MCP)
server — the interface between MCP clients (agents, editors, CLIs) and the kern MCP
server implemented in [`internal/mcp/`](../../internal/mcp/).

A **contract** is the promise the server makes to its clients: the tools it exposes,
the shape of their inputs, the wire protocol it speaks, and how both evolve over time.
Stable contracts mean a client written against this documentation keeps working as
kern changes internally.

## What kern's MCP server is

`kern-mcp` is a local, index-backed MCP server. It exposes kern's code-intelligence,
context-optimization, planning, execution, governance and security capabilities as MCP
tools, so any MCP-capable agent can query a codebase through kern's prebuilt symbol
index instead of re-reading files. All tools run locally; the only network call in
kern is the explicit, user-invoked `kern_doc_fetch`.

## Document index

| Document | Contents |
|---|---|
| [`tool-contracts.md`](tool-contracts.md) | The authoritative catalog of all 145 MCP tools: name, phase, risk level, description, parameters, required parameters, and JSON-RPC usage examples. |
| [`protocol.md`](protocol.md) | The wire protocol: transports (stdio / HTTP), JSON-RPC methods, capabilities, error handling, governance gates, and shutdown behavior. |
| [`versioning.md`](versioning.md) | Versioning policy: supported MCP protocol versions, server version reporting, tool-catalog stability rules, and how clients should negotiate. |

## Catalog at a glance

- **145 tools** across six phases: explore, plan, edit, verify,
  meta, cross.
- **Risk levels** on every tool: low (82), medium (31), high (18), critical (5) —
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

1. **New to kern MCP?** Read this README, then skim `tool-contracts.md` for the catalog
   and `protocol.md` for how to connect.
2. **Integrating a client?** `protocol.md` has the handshake, transports and error
   contract. `versioning.md` tells you how to negotiate versions.
3. **Looking up a tool?** `tool-contracts.md` is the reference — one section per tool.

## Related

- [`docs/authorized-context.md`](../authorized-context.md) — governed-mode context
  authorization (`kern_authorize_context`).
- [`ARCHITECTURE.md`](../../ARCHITECTURE.md) — system architecture and package subsystem boundaries.
- [`internal/mcp/usage_guide.go`](../../internal/mcp/usage_guide.go) — the `kern guide`
  text: phase shortlists, performance tiers and pitfalls (served verbatim to the CLI
  and the opencode plugin).