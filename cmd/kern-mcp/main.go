// Command kern-mcp runs the kern MCP server over stdio (default) or HTTP
// (--http ADDR). The HTTP transport is Streamable HTTP style: POST JSON-RPC
// messages to /mcp and read the response body.
//
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
	"github.com/JayveerPrajapati/kern/internal/stats"
)

// version is stamped at build time via -ldflags "-X main.version=v1.2.3".
var version = "dev"

func main() {
	httpAddr := flag.String("http", "", "serve MCP over HTTP on this address (e.g. :8080) instead of stdio")
	flag.Parse()
	mcp.SetServerVersion(version)
	rec, err := stats.NewRecorder()
	if err == nil {
		optimize.Recorder = rec
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *httpAddr != "" {
		if err := mcp.ServeHTTPContext(ctx, *httpAddr); err != nil {
			os.Exit(1)
		}
		return
	}
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	go func() {
		<-ctx.Done()
		srv.CancelAll()
		_ = os.Stdin.Close()
	}()
	if err := srv.Serve(); err != nil {
		os.Exit(1)
	}
	srv.CancelAll()
}
