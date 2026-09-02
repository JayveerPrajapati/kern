// Command kern-mcp runs the kern MCP server over stdio (default) or HTTP
// (--http ADDR). The HTTP transport is Streamable HTTP style: POST JSON-RPC
// messages to /mcp and read the response body.
// SIGINT/SIGTERM trigger a graceful shutdown: in-flight tool calls are
// cancelled (their child processes killed), held locks are released, and the
// server stops reading input. This keeps slow tools from hanging the process
// until the OS force-kills it.
package main

import (
	"context"
	"flag"
	"os"
	"os/signal"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/mcp"
	"github.com/JayveerPrajapati/kern/internal/optimize"
	kversion "github.com/JayveerPrajapati/kern/internal/version"
)

// version is the build-stamped release version, initialized from the shared
// internal/version.Version so every kern binary reports the same value.
// It starts as the literal "dev" (not a copy of kversion.Version) because
// the legacy -ldflags "-X main.version=..." only rewrites a variable whose
// initializer is a compile-time constant: a runtime copy from another global
// aliases the read and silently defeats -X. When unstamped, init() adopts
// the shared internal/version.Version (default "dev", or the newer
// "-X github.com/JayveerPrajapati/kern/internal/version.Version=..." form).
var version = "dev"

func init() {
	if version == "dev" {
		version = kversion.Version
	}
}

func main() {
	httpAddr := flag.String("http", "", "serve MCP over HTTP on this address (e.g. :8080) instead of stdio")
	flag.Parse()
	mcp.SetServerVersion(version)
	_ = optimize.EnsureRecorder()
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *httpAddr != "" {
		if err := mcp.ServeHTTPContext(ctx, *httpAddr); err != nil {
			os.Exit(1)
		}
		return
	}
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	// ServeStdio owns the SIGINT/SIGTERM drain (cancel in-flight tools,
	// release locks, close stdin, wait up to 5s for in-flight calls). It
	// never calls os.Exit: a clean drain returns nil (exit 0), a drain
	// timeout or serve error returns a non-nil error (exit 1), so deferred
	// cleanup runs on every path.
	if err := mcp.ServeStdio(srv); err != nil {
		os.Exit(1)
	}
}
