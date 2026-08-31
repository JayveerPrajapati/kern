package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

func runMCP(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	httpAddr := mcpHTTPAddr(args, f)
	wireRecorder()
	mcp.SetServerVersion(version)
	if httpAddr != "" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		if err := mcp.ServeHTTPContext(ctx, httpAddr); err != nil {
			// Route through the exitError sentinel so main() persists
			// metrics before the real exit (same exit code 1 as before).
			panic(exitError{code: 1})
		}
		return
	}
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	// ServeStdio owns the SIGINT/SIGTERM drain (cancel in-flight tools,
	// release locks, close stdin, wait up to 5s for in-flight calls). It
	// never calls os.Exit: a clean drain returns nil (exit 0), a drain
	// timeout or serve error returns a non-nil error (exit 1) routed through
	// the exitError sentinel so main() persists metrics before the real exit.
	if err := mcp.ServeStdio(srv); err != nil {
		panic(exitError{code: 1})
	}
}
