package main

import (
	"context"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/mcp"
)

func runMCP(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	httpAddr := mcpHTTPAddr(args, f)
	if len(f.projects) > 0 {
		var roots []string
		for _, p := range f.projects {
			_, root, ok := strings.Cut(p, "=")
			if !ok || root == "" {
				root = p
			}
			roots = append(roots, root)
		}
		existing := os.Getenv("KERN_MCP_ROOTS")
		if existing != "" {
			roots = append(roots, strings.Split(existing, ",")...)
		}
		os.Setenv("KERN_MCP_ROOTS", strings.Join(roots, ","))
		os.Setenv("KERN_ROOTS", strings.Join(roots, ","))
	}
	wireRecorder()
	mcp.SetServerVersion(version)
	if httpAddr != "" {
		ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
		defer cancel()
		// TLS is optional: --tls-cert/--tls-key flags win, KERN_MCP_TLS_CERT /
		// KERN_MCP_TLS_KEY env vars fill in what the flags leave empty.
		tlsCfg := mcp.TLSOptions(f.tlsCert, f.tlsKey)
		if err := mcp.ServeHTTPContextWithTLS(ctx, httpAddr, tlsCfg); err != nil {
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
