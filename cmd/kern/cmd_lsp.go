package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/lsp"
)

// runLSP serves the Language Server Protocol over stdio (G-8), backed by the
// prebuilt symbol index. It mirrors cmd_mcp.go's graceful pattern: SIGINT /
// SIGTERM cancel a NotifyContext that lsp.Serve observes (closing stdin to
// unblock the read loop), so the process exits 0 on a clean drain.
func runLSP(rest []string) {
	f, _, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	root := f.root
	if root == "" {
		root = "."
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	// Serve owns stdout exclusively (the LSP protocol channel); all its
	// diagnostics go to stderr, so nothing here may print to stdout either.
	if err := lsp.Serve(ctx, root, os.Stdin, os.Stdout); err != nil {
		fatal("lsp: %v", err)
	}
}
