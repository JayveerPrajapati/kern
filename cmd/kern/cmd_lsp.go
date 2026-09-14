package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/JayveerPrajapati/kern/internal/lsp"
	"github.com/JayveerPrajapati/kern/internal/lspbridge"
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

// runLSPBridge handles the CLI query interface for the zero-weight LSP client bridge.
func runLSPBridge(rest []string) {
	f, args, err := parseFlags(rest)
	if err != nil {
		fatalUsage("flags: %v", err)
	}
	file := f.file
	if file == "" && len(args) > 0 {
		file = args[0]
	}
	action := f.action
	if action == "" {
		action = "definition"
	}
	root := f.root
	if root == "" {
		root = "."
	}

	line := f.lines
	if line <= 0 {
		line = 1
	}

	col := f.column
	if col <= 0 {
		col = 1
	}

	var customCmd []string
	if f.serverCmd != "" {
		customCmd = strings.Fields(f.serverCmd)
	}

	req := lspbridge.QueryRequest{
		Root:      root,
		File:      file,
		Line:      line,
		Column:    col,
		Action:    action,
		ServerCmd: customCmd,
	}

	res, err := lspbridge.Query(context.Background(), req)
	if err != nil {
		fatal("lsp-bridge: %v", err)
	}

	if f.json {
		printJSON(res)
		return
	}

	fmt.Printf("=== LSP Bridge: %s ===\n", strings.ToUpper(res.Action))
	if res.LanguageServer != "" {
		fmt.Printf("Server:   %s (%s)\n", res.LanguageServer, res.Language)
	}
	if res.File != "" {
		fmt.Printf("Target:   %s:%d:%d\n", res.File, line, col)
	}
	if res.Warning != "" {
		fmt.Printf("Warning:  %s\n", res.Warning)
	}
	if res.Error != "" {
		fmt.Printf("Error:    %s\n", res.Error)
	}

	if res.Hover != nil {
		fmt.Println("\n--- Hover / Type Signature ---")
		if res.Hover.Signature != "" {
			fmt.Printf("```\n%s\n```\n", res.Hover.Signature)
		}
		if res.Hover.Doc != "" {
			fmt.Println(res.Hover.Doc)
		}
	}

	if len(res.Locations) > 0 {
		fmt.Printf("\n--- Locations (%d) ---\n", len(res.Locations))
		for _, loc := range res.Locations {
			fmt.Printf("- %s:%d:%d\n", loc.File, loc.Line, loc.Col)
		}
	}

	if len(res.Symbols) > 0 {
		fmt.Printf("\n--- Document Symbols (%d) ---\n", len(res.Symbols))
		var printSym func(sym lspbridge.SymbolInfo, depth int)
		printSym = func(sym lspbridge.SymbolInfo, depth int) {
			indent := strings.Repeat("  ", depth)
			detail := ""
			if sym.Detail != "" {
				detail = fmt.Sprintf(" (%s)", sym.Detail)
			}
			fmt.Printf("%s- [%s] %s%s (line %d)\n", indent, sym.Kind, sym.Name, detail, sym.Range.Start.Line+1)
			for _, child := range sym.Children {
				printSym(child, depth+1)
			}
		}
		for _, sym := range res.Symbols {
			printSym(sym, 0)
		}
	}

	if res.Available != nil {
		fmt.Println("\n--- Detected / Installed Language Servers in PATH ---")
		printJSON(res.Available)
	}
}
