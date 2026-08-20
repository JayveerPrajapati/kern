package main

import (
	"context"
	"github.com/JayveerPrajapati/kern/internal/mcp"
	"os"
	"os/signal"
	"syscall"
	"time"
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
			os.Exit(1)
		}
		return
	}
	srv := mcp.NewServer(os.Stdin, os.Stdout)
	// On SIGINT/SIGTERM cancel in-flight tool calls and release locks so slow
	// tools can't hang the process. Closing os.Stdin doesn't reliably unblock
	// the scanner, so wait for in-flight calls to drain, then exit.
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
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
