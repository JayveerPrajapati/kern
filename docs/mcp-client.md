# External MCP Servers

Kern is a full MCP citizen: it serves 145 `kern_*` tools over
`kern mcp` / `cmd/kern-mcp`, and it can call tools from external MCP
servers through `kern_mcp_call`.

## Configure a server

Servers live in `.kern/mcp-servers.json` (gitignored). No server is enabled
by default.

```bash
# stdio transport (a local program speaking MCP on stdin/stdout)
kern mcp-client add github --transport stdio \
  --command npx --arg -y --arg @modelcontextprotocol/server-github \
  --env GITHUB_TOKEN=$GITHUB_TOKEN

# streamable-http transport (a remote MCP service)
kern mcp-client add web --transport streamable-http \
  --url http://localhost:3000/mcp \
  --header "Authorization=Bearer $MCP_TOKEN"
```

## Call a server tool

```bash
kern mcp-client call github create_issue '{"title": "bug", "body": "..."}'
```

Through the tool surface, `kern_mcp_call {server, tool, arguments}` bridges
the server's tool; you may pass the raw wire name or the public name
`mcp__<server>__<tool>`. Only the raw name is sent on the wire; public names
are normalized to `[A-Za-z0-9_-]` (max 64 chars, SHA-256 suffix on lossy
normalization).

## Scope

Tools only — MCP resources and prompts are unsupported. The client is
stdlib-only and always built; tools are reached through `kern_mcp_call`,
not registered as native `kern_*` tools.