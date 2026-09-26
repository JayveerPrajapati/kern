// Package lsp owns the language server bridge MCP tool bodies (kern_lsp_bridge)
// as plain functions.
package lsp

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/lspbridge"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
)

// Handle handles the kern_lsp_bridge tool request.
func Handle(ctx context.Context, args map[string]any) (string, error) {
	root := root.ResolveRoot(mcpargs.ArgString(args, "root"))
	file := mcpargs.ArgString(args, "file")
	action := mcpargs.ArgString(args, "action")
	serverCmdStr := mcpargs.ArgString(args, "server_cmd")
	format := mcpargs.ArgString(args, "format")

	line := 1
	if lStr := mcpargs.ArgString(args, "line"); lStr != "" {
		if n, err := strconv.Atoi(lStr); err == nil && n > 0 {
			line = n
		}
	}

	col := 1
	if cStr := mcpargs.ArgString(args, "column"); cStr != "" {
		if n, err := strconv.Atoi(cStr); err == nil && n > 0 {
			col = n
		}
	}

	var customCmd []string
	if serverCmdStr != "" {
		customCmd = strings.Fields(serverCmdStr)
	}

	req := lspbridge.QueryRequest{
		Root:      root,
		File:      file,
		Line:      line,
		Column:    col,
		Action:    action,
		ServerCmd: customCmd,
	}

	res, err := lspbridge.Query(ctx, req)
	if err != nil {
		return "", fmt.Errorf("lsp query failed: %w", err)
	}

	if format == "json" {
		data, err := json.MarshalIndent(res, "", "  ")
		if err != nil {
			return "", err
		}
		return string(data), nil
	}

	// Render human/agent readable text format
	var b strings.Builder
	b.WriteString(fmt.Sprintf("=== LSP Bridge: %s ===\n", strings.ToUpper(res.Action)))
	if res.LanguageServer != "" {
		b.WriteString(fmt.Sprintf("Server:   %s (%s)\n", res.LanguageServer, res.Language))
	}
	if res.File != "" {
		b.WriteString(fmt.Sprintf("Target:   %s:%d:%d\n", res.File, line, col))
	}
	if res.Warning != "" {
		b.WriteString(fmt.Sprintf("Warning:  %s\n", res.Warning))
	}
	if res.Error != "" {
		b.WriteString(fmt.Sprintf("Error:    %s\n", res.Error))
	}

	if res.Hover != nil {
		b.WriteString("\n--- Hover / Type Signature ---\n")
		if res.Hover.Signature != "" {
			b.WriteString(fmt.Sprintf("```\n%s\n```\n", res.Hover.Signature))
		}
		if res.Hover.Doc != "" {
			b.WriteString(fmt.Sprintf("%s\n", res.Hover.Doc))
		}
	}

	if len(res.Locations) > 0 {
		b.WriteString(fmt.Sprintf("\n--- Locations (%d) ---\n", len(res.Locations)))
		for _, loc := range res.Locations {
			b.WriteString(fmt.Sprintf("- %s:%d:%d\n", loc.File, loc.Line, loc.Col))
		}
	}

	if len(res.Symbols) > 0 {
		b.WriteString(fmt.Sprintf("\n--- Document Symbols (%d) ---\n", len(res.Symbols)))
		var printSym func(sym lspbridge.SymbolInfo, depth int)
		printSym = func(sym lspbridge.SymbolInfo, depth int) {
			indent := strings.Repeat("  ", depth)
			detail := ""
			if sym.Detail != "" {
				detail = fmt.Sprintf(" (%s)", sym.Detail)
			}
			b.WriteString(fmt.Sprintf("%s- [%s] %s%s (line %d)\n", indent, sym.Kind, sym.Name, detail, sym.Range.Start.Line+1))
			for _, child := range sym.Children {
				printSym(child, depth+1)
			}
		}
		for _, sym := range res.Symbols {
			printSym(sym, 0)
		}
	}

	if res.Available != nil {
		b.WriteString("\n--- Detected / Installed Language Servers in PATH ---\n")
		raw, _ := json.MarshalIndent(res.Available, "", "  ")
		b.WriteString(string(raw) + "\n")
	}

	return b.String(), nil
}
