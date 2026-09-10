# Kern MCP Protocol

This document defines the wire protocol of the kern MCP server: transports,
JSON-RPC methods, capabilities, error handling, governance gates and lifecycle
behavior. It is the contract a client must implement to talk to `kern-mcp`.

## 1. Transport

The server speaks **JSON-RPC 2.0** over one of two transports.

### 1.1 stdio (default)

`kern mcp` (or the `kern-mcp` binary with no flags) serves over the process's
`stdin`/`stdout`. Messages are newline-delimited JSON-RPC 2.0 frames — one request
per line on stdin, one response per line on stdout. This is the transport used by
Claude Code, Cursor, Gemini, Copilot and Qwen integrations.

```
kern mcp                      # stdio mode
kern-mcp                      # stdio mode (binary)
```

### 1.2 Streamable HTTP (opt-in)

`kern-mcp --http ADDR` serves Streamable HTTP: **POST JSON-RPC messages to `/mcp`**
and read the response body. A `/health` endpoint reports liveness.

```
kern-mcp --http :8080                                   # plain HTTP
kern-mcp --http :8080 --tls-cert cert.pem --tls-key key.pem   # TLS
```

- The listener binds to **loopback only**; non-loopback binds are rejected for
  security (`kern-server` is the network-facing binary).
- An HTTP **Origin check** applies on every request.
- **TLS is optional and opt-in**: `--tls-cert` / `--tls-key` flags, or the
  `KERN_MCP_TLS_CERT` / `KERN_MCP_TLS_KEY` environment variables. TLS protects the
  transport against loopback sniffing; the loopback bind and Origin check remain.
- The initialize response advertises `streamableHttpCapabilities: {sse: false}`.

### 1.3 Graceful shutdown

On SIGINT/SIGTERM the server cancels in-flight tool calls (killing child processes),
releases locks, closes stdin and waits up to **5 seconds** for in-flight calls to
drain. A clean drain exits 0; a drain timeout exits 1 (`ErrDrainTimeout`). Clients
should treat an EOF/connection close after a signal as a normal shutdown.

## 2. Protocol versions

The server implements MCP protocol version **`2025-06-18`** and accepts the
following versions in the `initialize` handshake (see `versioning.md`):

| Protocol version | Supported |
|---|---|
| `2024-11-05` | yes |
| `2025-03-26` | yes |
| `2025-06-18` | yes (default) |

## 3. JSON-RPC methods

All requests are JSON-RPC 2.0 with a numeric `id`; notifications omit `id`.

### 3.1 `initialize`

```json
{"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"my-client","version":"1.0"}}}
```

Response:

```json
{"jsonrpc":"2.0","id":1,"result":{
  "protocolVersion":"2025-06-18",
  "capabilities":{
    "tools":{"listChanged":false},
    "prompts":{"listChanged":false},
    "streamableHttpCapabilities":{"sse":false}
  },
  "serverInfo":{"name":"kern","version":"0.9.8"}
}}
```

- `protocolVersion` echoes the client's version when supported, otherwise reports
  `2025-06-18`.
- `serverInfo.name` is always `kern`; `serverInfo.version` is the build-stamped
  release version (see `versioning.md`).
- `listChanged: false` — the tool and prompt lists are static per server lifetime;
  clients should not poll for changes.
- On the first connection the server kicks off background index preloading;
  index-backed tools may block briefly until the build finishes.

### 3.2 `notifications/initialized`

Sent by the client after a successful `initialize`. No response.

### 3.3 `ping`

```json
{"jsonrpc":"2.0","id":2,"method":"ping"}
```
→ `{"jsonrpc":"2.0","id":2,"result":{}}`

### 3.4 `tools/list`

```json
{"jsonrpc":"2.0","id":3,"method":"tools/list"}
```
→ `{"jsonrpc":"2.0","id":3,"result":{"tools":[...]}}`

Each tool entry has the shape:

```json
{
  "name": "kern_code_graph",
  "description": "Return the call graph neighbourhood of a symbol...",
  "inputSchema": {
    "type": "object",
    "properties": {"symbol": {"type": "string", "description": "..."}},
    "required": ["symbol"]
  },
  "phase": "explore",
  "riskLevel": "low"
}
```

`phase` and `riskLevel` are kern-specific extensions on every tool. The advertised
list is filtered by the server's phase configuration (§5); `kern_meta` and all
`cross` tools are always present.

### 3.5 `tools/call`

```json
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"kern_code_graph","arguments":{"symbol":"User.Login","root":"/path/to/project"}}}
```

Responses are MCP content blocks:

```json
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"..."}],"isError":false}}
```

- Tool output is returned as text content; failures set `isError: true`.
- Output is PII/secret-masked before return where the tool is configured to do so.
- A `max_output` argument (on tools that support it) raises the output sandbox cap;
  `0` disables it.

### 3.6 `prompts/list` and `prompts/get`

The server also exposes a small set of prompts:

| Prompt | Purpose |
|---|---|
| `review_changes` | Code-review a git range |
| `architecture_map` | Map a project's architecture |
| `debug_issue` | Debug a bug report / panic with optional trace |
| `onboard_developer` | Onboard a developer to a repo |
| `pre_merge_check` | Pre-merge checklist for a git range |
| `refactor_symbol` | Rename a Go package-level symbol |
| `compute_quick_answer` | Compute a self-contained answer without tools |

```json
{"jsonrpc":"2.0","id":5,"method":"prompts/list"}
{"jsonrpc":"2.0","id":6,"method":"prompts/get","params":{"name":"review_changes","arguments":{"range":"HEAD~2..HEAD"}}}
```

## 4. Errors

Errors use standard JSON-RPC error objects: `{"jsonrpc":"2.0","id":N,"error":{"code":C,"message":"..."}}`.

| Code | Meaning |
|---|---|
| `-32601` | Method not found (`method not found: <method>`) |
| `-32600` | Invalid request |
| `-32700` | Parse error |
| other | Tool-call failures (reported as `isError: true` in the result, or as JSON-RPC errors) |

## 5. Tool catalog configuration

The advertised catalog is configurable at server start. This changes the *advertised
surface*, never the underlying capability — `kern_meta`'s router reaches every
sub-tool regardless.

| Env var | Effect |
|---|---|
| `KERN_MCP_PHASE` | `explore` \| `plan` \| `edit` \| `verify` — advertise only that phase's shortlist plus always-on meta/cross tools |
| `KERN_MCP_FULL=1` | Advertise the full 121-tool catalog (default is the minimal ~11-tool surface) |
| `KERN_MCP_SINGLE_TOOL=1` | Advertise only `kern_meta` |
| `KERN_MCP_ROOTS` | Comma-separated allowed workspace roots for the path gate (§6); also configurable as `mcp.roots` in `.kern/config.json` |
| `KERN_MCP_PERMISSIVE=1` | Opt out of the path-confinement gate |
| `KERN_MCP_WATCH=0` | Disable the background index watcher |
| `KERN_MCP_WATCH_INTERVAL` | Index watcher poll interval |
| `KERN_MCP_TLS_CERT` / `KERN_MCP_TLS_KEY` | TLS material for the HTTP transport |

## 6. Governance gates

### 6.1 Path confinement (always on)

Every tool call's path-typed arguments (`root`, `dir`, `*path*`) are resolved and
checked against the allowed roots before the handler runs. A call that resolves
outside the roots is **rejected**. The gate fails closed: with no `KERN_MCP_ROOTS`
configured it confines to the server's working directory, so a zero-config
deployment is confined to the workspace rather than trusted unconditionally.
`KERN_MCP_PERMISSIVE=1` is the explicit opt-out.

### 6.2 Command execution

`kern_exec`, `kern_sandbox` and related tools are gated by the command-execution
governance firewall (`KERN_ALLOW_EXEC` / `KERN_TOOLS`). Denied calls return an
error explaining the missing allowance.

### 6.3 Risk levels

Every tool carries a `riskLevel` so governed clients can gate access:

| Risk | Meaning | Count |
|---|---|---|
| `low` | Read-only | 68 |
| `medium` | Contained state mutation or analysis | 30 |
| `high` | Security-sensitive or destructive | 14 |
| `critical` | Arbitrary command execution or deployment | 5 |

## 7. Phase model

Tools are tagged with an agent phase: `explore`, `plan`, `edit`, `verify`, plus
`meta` (the `kern_meta` router itself) and `cross` (phase-agnostic utilities).
Clients that know the current phase can advertise only the relevant shortlist;
`kern_meta` remains available to route within it by natural-language request.

## 8. Connecting a client

```
kern setup --agents mcp,opencode,claude   # wire kern into agents (idempotent)
kern setup --verify                       # spawn kern-mcp and check the initialize handshake
```

`kern setup --verify` is the recommended smoke test: it spawns the configured
`kern-mcp` and asserts it answers the MCP `initialize` handshake correctly.