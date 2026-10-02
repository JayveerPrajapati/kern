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

The catalog is the contract clients depend on most: 117 tools at HEAD, registered in
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
- The catalog count (117) and the `ToolNames()` set are enforced by catalog-parity
  invariants (plugin ↔ MCP, docs ↔ MCP) — a change to the registration table that
  breaks parity fails CI.

### Breaking changes

- **2026-10-01 — memory tool consolidation + `kern_do` (117 tools).** The four
  memory tools `kern_memory_add`, `kern_memory_list`, `kern_memory_recall` and
  `kern_memory_ranked` were removed and replaced by a single `kern_memory` tool
  that dispatches on a required `action` parameter (`add|list|recall|ranked|
  remove|clear`); `kern_do` was added alongside `kern_loop`. This is a
  **deliberate hard removal** (maintainer-approved) that deviates from the
  ≥1-minor-release deprecation policy above: the four removed names fail with
  "unknown tool" immediately, and clients must migrate to
  `kern_memory {action=add|list|recall|ranked|remove|clear}`.
- **2026-10-01 — 8-family action-arg consolidation (117 tools).** Twenty-six
  tools were removed and replaced by eight consolidated tools that dispatch on
  a required `action` (or `entity`) parameter: `kern_agent`
  (action=message|interrupt|fingerprint|coordination|rbac) replaces
  `kern_agent_message`, `kern_agent_interrupt`, `kern_agent_fingerprint`,
  `kern_agent_coordination` and `kern_agent_role_rbac`; `kern_doc`
  (action=search|fetch|index) replaces `kern_doc_search`, `kern_doc_fetch`
  and `kern_doc_index`; `kern_evidence` gains action=anchor (replacing
  `kern_evidence_anchor`); `kern_lock` (action=acquire|release|status)
  replaces `kern_lock`, `kern_unlock` and `kern_lock_status`; `kern_optimize`
  (action=prompt|log|output) replaces `kern_optimize_prompt`,
  `kern_optimize_log` and `kern_optimize_output`; `kern_org`
  (entity=projects|agents|teams|memory|tasks|search|audit|user) replaces the
  eight `kern_org_*` tools; `kern_repair` (action=diagnostics|guidance)
  replaces `kern_repair_diagnostics` and `kern_repair_guidance`; and
  `kern_semantic` (action=diff|merge) replaces `kern_semantic_diff` and
  `kern_semantic_merge`. This is a **deliberate hard removal**
  (maintainer-approved) that deviates from the ≥1-minor-release deprecation
  policy above: the twenty-six removed names fail with "unknown tool"
  immediately, and clients must migrate `KERN_TOOLS` allowlists to the
  consolidated names (e.g. `kern_optimize_prompt` → `kern_optimize`,
  `kern_repair_diagnostics` → `kern_repair`, `kern_doc_search` → `kern_doc`).

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