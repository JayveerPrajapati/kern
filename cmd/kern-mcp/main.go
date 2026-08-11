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
	"time"
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
	// Closing os.Stdin from another goroutine does not reliably unblock the
	// scanner's read, so Serve() alone may never return after a signal:
	// cancel in-flight tools, wait for them to drain, then exit (W2-41).
	go func() {
		<-ctx.Done()
		srv.CancelAll()
		srv.Close()
		_ = os.Stdin.Close()
		deadline := time.Now().Add(5 * time.Second)
		for time.Now().Before(deadline) {
			if srv.Inflight() == 0 {
				time.Sleep(100 * time.Millisecond)
				os.Exit(0)
			}
			time.Sleep(50 * time.Millisecond)
		}
		os.Exit(1)
	}()
	if err := srv.Serve(); err != nil {
		os.Exit(1)
	}
	srv.CancelAll()
	srv.Close()
}
