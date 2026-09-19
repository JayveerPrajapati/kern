# Kern MCP Versioning Policy

This document defines how kern's MCP contracts are versioned and what stability
clients can rely on. There are three independent version axes:

1. **MCP protocol version** — the wire protocol (JSON-RPC methods, framing).
2. **Server version** — the kern release (`serverInfo.version`).
3. **Tool catalog** — the set of tools, their names, parameters and metadata.

## 1. MCP protocol version

The server implements MCP protocol version **`2025-06-18`** (constant
`protocolVersion` in `internal/mcp/server.go`) and accepts three protocol versions
in the `initialize` handshake:

| Protocol version | Status |
|---|---|
| `2024-11-05` | supported |
| `2025-03-26` | supported |
| `2025-06-18` | **current default** |

### Negotiation

The client sends its `protocolVersion` in `initialize`. The server:

- echoes the client's version when it is one of the supported set, or
- reports `2025-06-18` (the version it implements) otherwise.

Because the wire format is shared across the supported versions, a client
negotiating any of them can talk to this server.

### Policy

- **Additions** (new JSON-RPC methods, new capabilities) are always backwards
  compatible: the server must keep answering all previously supported methods.
- **Dropping** a protocol version is a **breaking change** requiring a major kern
  release, and must be preceded by a deprecation notice in this document and in the
  changelog.
- Clients SHOULD send the highest protocol version they implement and MUST tolerate
  a server that responds with a lower (or its own) version.

## 2. Server version

`serverInfo.version` in the `initialize` response reports the kern release version.

- Source builds without ldflags report **`dev`**.
- Release builds stamp the version at build time:
  `-ldflags "-X github.com/JayveerPrajapati/kern/internal/version.Version=vX.Y.Z"`
  (the legacy `-X main.version=...` form is still honored; every binary derives its
  version from the shared `internal/version.Version`).
- Release tags follow a semver-style `vX.Y.Z` scheme (current line: `v0.9.x`, with
  patch releases such as `v0.9.5.1`).

### Policy

- The server version is **informational**: clients should log/display it, not branch
  behavior on it. Behavior contracts are the protocol version and the tool catalog.
- A change in the server version alone never breaks a conforming client.

## 3. Tool catalog

The catalog is the contract clients depend on most: 146 tools at HEAD, registered in
`internal/mcp/tools.go` and served via `tools/list`. Every tool entry carries
`name`, `description`, `inputSchema`, `phase` and `riskLevel`.

### Compatibility rules

| Change | Class | Policy |
|---|---|---|
| New tool added | additive | **minor/patch** — allowed in any release |
| New optional parameter on a tool | additive | **minor/patch** |
| New value in an existing parameter's semantics (e.g. a new `tier` value) | additive | **minor/patch** |
| New phase or risk metadata value | additive | **minor/patch** |
| New JSON-RPC capability | additive | **minor/patch** |
| Tool removed | breaking | **major release** only, with prior deprecation |
| Parameter removed or made required | breaking | **major release** only, with prior deprecation |
| Existing parameter's meaning changed incompatibly | breaking | **major release** only, with prior deprecation |
| `riskLevel` lowered (e.g. `critical` → `high`) | conservative | allowed at any time — it only *loosens* gating |
| `riskLevel` raised (e.g. `low` → `high`) | breaking for governed clients | **major release** only, with prior deprecation |

### Deprecation policy

- A tool or parameter being removed is first marked **deprecated** in its
  `description` (and in `tool-contracts.md`) for at least one minor release.
- Deprecated tools keep working until their removal release.
- The catalog count (146) and the `ToolNames()` set are enforced by catalog-parity
  invariants (plugin ↔ MCP, docs ↔ MCP) — a change to the registration table that
  breaks parity fails CI.

### `kern_meta` routing

`kern_meta` routes natural-language requests to sub-tools. Its routing is an
implementation detail: clients MUST NOT depend on which sub-tool a given request
invokes. The advertised surface (`KERN_MCP_PHASE`, `KERN_MCP_FULL`,
`KERN_MCP_SINGLE_TOOL`) may change without a catalog-version bump; the underlying
capability set is unchanged.

## 4. Configuration stability

Server configuration knobs are part of the contract:

- Environment variables (`KERN_MCP_PHASE`, `KERN_MCP_FULL`, `KERN_MCP_SINGLE_TOOL`,
  `KERN_MCP_ROOTS`, `KERN_MCP_PERMISSIVE`, `KERN_MCP_WATCH`,
  `KERN_MCP_WATCH_INTERVAL`, `KERN_MCP_TLS_CERT`, `KERN_MCP_TLS_KEY`,
  `KERN_ALLOW_EXEC`, `KERN_TOOLS`) are stable once documented in `protocol.md`.
- The `mcp.roots` config key in `.kern/config.json` mirrors `KERN_MCP_ROOTS`.
- Adding a new variable is additive; renaming or removing a documented variable is a
  breaking change requiring a major release.

## 5. Document contract

- `tool-contracts.md` is generated from `internal/mcp/tools.go` — the registration
  table is the source of truth, and regenerating the doc must not change any tool's
  contract.
- `protocol.md` documents the wire behavior; changes to the wire behavior must be
  reflected there in the same release.
- A client that conforms to the latest `docs/mcp/` set and the protocol version it
  negotiated is guaranteed compatibility under the rules above.

## 6. Summary for clients

1. Send your highest supported `protocolVersion` in `initialize`; trust the echoed value.
2. Treat `serverInfo.version` as informational.
3. Only rely on tools and parameters documented in `tool-contracts.md`.
4. Treat unknown tools, parameters, phases and risk levels as future additive
   changes — ignore them gracefully.
5. Treat removal, repurposing or `riskLevel` raises as breaking changes gated behind
   major releases — if one appears without a major bump, it is a bug.