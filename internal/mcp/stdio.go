package mcp

import (
	"github.com/JayveerPrajapati/kern/internal/mcp/transport"
)

// ErrDrainTimeout is returned by ServeStdio when a SIGINT/SIGTERM shutdown is
// requested but in-flight tool calls do not drain within the 5-second
// deadline. Callers should exit with a non-zero status. The value aliases
// transport.ErrDrainTimeout, which owns the drain choreography.
var ErrDrainTimeout = transport.ErrDrainTimeout

// ServeStdio serves the MCP server over the process's stdin/stdout and
// blocks until the server exits; on SIGINT/SIGTERM it cancels in-flight
// tool calls and waits up to 5s for them to drain. The signal handling and
// drain choreography live in transport.ServeStdio; this wrapper is the
// composition point that supplies the env-derived background-watch interval
// (KERN_MCP_WATCH / KERN_MCP_WATCH_INTERVAL) and adapts *Server to the
// transport.StdioServer contract structurally.
func ServeStdio(srv *Server) error {
	return transport.ServeStdio(srv, watchIntervalFromEnv())
}
