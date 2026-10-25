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

- Frames must be valid JSON-RPC 2.0 envelopes: a line that parses but does
  not carry `"jsonrpc":"2.0"` exactly is answered `-32600` (invalid request).
- Lines over **16 MiB** are drained and skipped without killing the session —
  the same cap the HTTP transport enforces with `413`.
- Unsolicited JSON-RPC *response* frames sent to the server are ignored
  silently (JSON-RPC defines no server-side error for them).

### 1.2 Streamable HTTP (opt-in)

`kern-mcp --http ADDR` serves Streamable HTTP: **POST JSON-RPC messages to `/mcp`**
and read the response body. A `/health` endpoint reports liveness.

```
kern-mcp --http auto                                          # default: 0600 unix socket (recommended)
kern-mcp --http unix:/tmp/kern-mcp.sock                       # explicit unix socket
kern-mcp --http :8080                                         # explicit loopback TCP
kern-mcp --http :8080 --tls-cert cert.pem --tls-key key.pem   # TLS
```

- **Transport selection.** An explicit unix path (`unix:PATH`, `/PATH`, `*.sock`)
  or an explicit TCP address (`:8080`, `host:port`) is honored as given. The
  sentinel `auto` (or `uds`), and an empty address from library callers,
  auto-selects a **unix domain socket in a fresh `0700` temp dir**, with the
  socket file created `0600` — reachable only by the owning user, whereas a
  loopback TCP port is reachable by *any* local process on a multi-user host.
  The chosen socket path is printed to stderr so clients can connect. The
  auto-selection falls back to loopback TCP on `127.0.0.1:8080` only on Windows
  (no unix sockets) or when the legacy behavior is forced with
  `KERN_MCP_TRANSPORT=tcp`. The socket is unlinked on clean shutdown.
- Explicit TCP binds are **loopback only**; non-loopback binds are rejected for
  security.
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
- Background index preloading starts at serve start (§8.2), so a fresh
  root's build is underway before the client's first request; index-backed
  tools on a never-indexed root may still block until that first build
  finishes.

### 3.2 `notifications/initialized`

Sent by the client after a successful `initialize`. No response.

The spec's `notifications/cancelled` (params `{"requestId": ..., "reason": ...}`)
is accepted as an alias of the JSON-RPC `$/cancelRequest` method and cancels
the in-flight request with that id.

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

Payload size (accepted, documented floor): the default 32-tool surface
serializes to roughly 41KB (~10K tokens) because descriptions, enums,
and per-property contracts are served in full — schema fidelity is the
product. `listChanged: false` (§3.1) means a client fetches this once
per session and caches it; the cost is per connection, not per request.
Hosts that want a smaller transport payload can shrink the surface
itself (`KERN_MCP_FULL`, `KERN_MCP_PHASE` — see `README.md`); there is
no schema-lite mode.

Each tool entry has the shape:

```json
{
  "name": "kern_graph",
  "description": "One-call graph context: names-only adjacency for a symbol...",
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
{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"kern_graph","arguments":{"symbol":"User.Login","root":"/path/to/project"}}}
```

Responses are MCP content blocks:

```json
{"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"..."}],"isError":false}}
```

- Tool output is returned as text content; failures set `isError: true`.
- Output is PII/secret-masked before return where the tool is configured to do so.
- No-match contract (automated consumers): a text result that begins with
  `no symbols matched` or `no strong symbol matches` is an authoritative
  negative — the tool found nothing for that query. These messages echo the
  raw query for operator context, so do NOT treat the appearance of the
  searched-for name in the text as a hit: result lines always begin with a
  symbol kind (`func `, `type `, `method `, ...), never with `no `.
  A no-match is a normal result (`isError: false`); the CLI equivalent
  signals the same state with exit code 1.
- Argument coercion contract (string-typed properties): a JSON number or
  boolean is coerced to its canonical string form (plugin/JSON callers
  legitimately pass numeric strings); `null`, object and array values are
  rejected as an `isError` naming the argument and the expected type. The
  path-typed `root` argument is stricter — a JSON number is rejected, never
  coerced (a numeric root is a client bug, not a path).
- A `max_output` argument (on tools that support it) raises the output sandbox cap;
  `0` disables it.
- Every successful `tools/call` result carries a `tokenMetadata` ledger:

  ```json
  "tokenMetadata": {"tokensUsed": 13, "tokensReturned": 112, "estimatedCost": 0.00125}
  ```

  `tokensUsed` is the token count of the request (tool name + serialized
  arguments); `tokensReturned` is the count of the final response text — the
  exact text the client sees, including already-served pointers and etag
  short-circuit replies. `estimatedCost` is the estimated **US dollars** for
  the call: `(tokensUsed + tokensReturned) × rate`, where the rate comes
  from `KERN_COST_PER_TOKEN` / config `cost_per_token`
  (default `$0.00001`/token). Counts use the active tokenizer (exact BPE
  when its data file is available, else the chars-per-kind estimator), so
  cross-checks against a different tokenizer may differ slightly.

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
| `KERN_MCP_FULL=1` | Advertise the full 32-tool catalog (default IS the full 32-tool catalog since 2026-10-23) |
| `KERN_MCP_SINGLE_TOOL=1` | Advertise only `kern_meta` |
| `KERN_MCP_HIGH_LEVEL_ONLY=1` | Legacy mid-size surface: the 6-tool `highLevelTools` set (deprecated; prefer the full catalog) |
| `KERN_MCP_CATEGORY=<family>` | Advertise only one functional family — one of `analyze`, `agent`, `arch`, `ast`, `context`, `doc`, `edit`, `evidence`, `exec`, `framework`, `graph`, `governance`, `incident`, `lock`, `memory`, `meta`, `mcpbridge`, `optimize`, `org`, `project`, `review`, `task`, `verify` (e.g. `kern_search` is `graph`); `kern_meta` always stays |
| `KERN_MCP_ROOTS` | Comma-separated allowed workspace roots for the path gate (§6); also configurable as `mcp.roots` in `.kern/config.json` |
| `KERN_MCP_PERMISSIVE=1` | Opt out of the path-confinement gate |
| `KERN_MCP_WATCH=0` | Disable the background index watcher |
| `KERN_MCP_WATCH_INTERVAL` | Index watcher poll interval |
| `KERN_MCP_TLS_CERT` / `KERN_MCP_TLS_KEY` | TLS material for the HTTP transport |
| `KERN_PRELOAD=0` | Disable the background index preload at serve start (§8.2) |

## 6. Governance gates

### 6.1 Path confinement (always on)

Every tool call's path-typed arguments (`root`, `dir`, `*path*`) are resolved and
checked against the allowed roots before the handler runs. A call that resolves
outside the roots is **rejected**. The gate fails closed: with no `KERN_MCP_ROOTS`
configured it confines to the server's working directory, so a zero-config
deployment is confined to the workspace rather than trusted unconditionally.
`KERN_MCP_PERMISSIVE=1` is the explicit opt-out.

### 6.2 Command execution

`kern_exec` is gated by the command-execution
governance firewall (`KERN_ALLOW_EXEC` / `KERN_TOOLS`). Denied calls return an
error explaining the missing allowance.

### 6.3 Risk levels

Every tool carries a `riskLevel` so governed clients can gate access:

| Risk | Meaning | Count |
|---|---|---|
| `low` | Read-only | 70 |
| `medium` | Contained state mutation or analysis | 25 |
| `high` | Security-sensitive or destructive | 18 |
| `critical` | Arbitrary command execution or deployment | 4 |

Counts sum to the 32-tool catalog (see `docs/tool-catalog.md`).

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

### 8.2 Fresh roots and cold start

Index-backed tools resolve a root through `index.LoadOrBuild`: a fresh
persisted `.kern` index is loaded immediately, and only missing/stale indexes
trigger a rebuild (debounced cross-process via the index-build lock). On a
**fresh, never-indexed root** the first build is a full scan and can take
minutes on a large repo; the server starts that build in the background at
serve start (§3.1), but the first index-backed tool call may still wait for it
to finish.

For a deploy, warm the index before the first client connects:

- `kern index <root>` / `kern onboard <root>` — build and persist the index
  ahead of time; the mandatory pre-step for a fresh repo.
- `kern-mcp --preindex <root>` — warm-then-serve: build and persist each
  listed root synchronously before the server accepts connections
  (repeatable; accepts `PATH` or `NAME=PATH`).

`KERN_PRELOAD=0` disables the serve-start preload; `KERN_MCP_WATCH=0` disables
the background staleness watcher.

## 9. Accepted deviations and boundaries

Recorded from the 2026-10-17 production-readiness audit. Each row is a
deliberate scope decision, declared through `capabilities` or answered with
the spec's correct error code — never a silent degradation.

| Area | Behavior | Why it is accepted |
|---|---|---|
| Resources API | Not implemented: `capabilities` does not advertise `resources`, and `resources/list` answers `-32601` method not found. | kern's data-equivalent for resources is the tool surface (context/evidence/memory bundles). An API that is not implemented is refused with the spec's error code rather than half-implemented. |
| SSE streaming | Not supported: initialize advertises `streamableHttpCapabilities: {sse: false}`, and a request with an `Accept: text/event-stream` header answers `501 Not Implemented`. | Every kern tool response is a single JSON-RPC result; SSE would add streaming complexity with no current consumer. |
| `2024-11-05` over HTTP | The legacy protocol version is accepted on the HTTP transport (pinned by `TestHandleHTTPSLegacyProtocolVersionAccepted`). | The 2024-11-05 wire format (JSON-RPC POST over the same method set) is shared with the newer versions; rejecting working legacy clients would break them for no security gain. |
| Notification responses | HTTP notifications answer `202 Accepted` with an empty body. | Spec-mandated (Streamable HTTP): a notification never receives a response body. |
| Request-body cap | Bodies over 16 MiB are rejected with `413 Content Too Large` before parsing (pinned by `TestHandleHTTPOversizeBodyRejected`). | A silently-truncated body must not be parsed into a misleading `-32700` parse error; the cap also bounds per-request memory. On stdio, a line over 16 MiB is drained to its newline and skipped without killing the session (the same cap; pinned by `TestStdioOversizeLineSkipped`). |
| Protocol-version set | Supported set is `{2024-11-05, 2025-03-26, 2025-06-18}`; an unsupported version answers `412 Precondition Failed`. | Honest negotiation: the server echoes only versions it actually implements. |