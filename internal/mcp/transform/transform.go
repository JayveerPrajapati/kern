// Package transform owns the AST transformation MCP tool bodies (kern_ast_transform)
// as plain functions.
package transform

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/transform"
)

// Hooks provides dependencies from the owning MCP server.
type Hooks struct {
	LoadIndex func(ctx context.Context, root string) (*index.Index, error)
}

func resolveRoot(root string) string {
	if root == "" {
		if cwd, err := os.Getwd(); err == nil {
			return filepath.Clean(cwd)
		}
		return "."
	}
	if abs, err := filepath.Abs(root); err == nil {
		return filepath.Clean(abs)
	}
	return root
}

// Transform executes an AST-level semantic mutation without fragile regex diffs.
func Transform(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	action := mcpargs.ArgString(args, "action")
	if action == "" {
		action = "implement_interface"
	}

	var ix *index.Index
	if mcpargs.ArgString(args, "code") == "" && h.LoadIndex != nil {
		ix, _ = h.LoadIndex(ctx, root)
	}

	req := transform.Request{
		Action:          action,
		File:            mcpargs.ArgString(args, "file"),
		Code:            mcpargs.ArgString(args, "code"),
		Root:            root,
		TargetSymbol:    mcpargs.ArgString(args, "target_symbol"),
		InterfaceName:   mcpargs.ArgString(args, "interface_name"),
		ReceiverName:    mcpargs.ArgString(args, "receiver_name"),
		ReceiverType:    mcpargs.ArgString(args, "receiver_type"),
		FieldName:       mcpargs.ArgString(args, "field_name"),
		FieldType:       mcpargs.ArgString(args, "field_type"),
		FieldTag:        mcpargs.ArgString(args, "field_tag"),
		MethodSignature: mcpargs.ArgString(args, "method_signature"),
		MethodBody:      mcpargs.ArgString(args, "method_body"),
		Apply:           mcpargs.ArgBool(args, "apply"),
		Index:           ix,
	}

	res, err := transform.Transform(req)
	if err != nil {
		return "", fmt.Errorf("ast transform: %w", err)
	}

	if mcpargs.ArgString(args, "format") == "json" {
		b, jerr := json.MarshalIndent(res, "", "  ")
		if jerr != nil {
			return "", jerr
		}
		return string(b), nil
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "# AST Transform Report: %s\n\n", res.Action)
	fmt.Fprintf(&sb, "- **Target Symbol**: `%s`\n", res.TargetSymbol)
	if res.File != "" {
		fmt.Fprintf(&sb, "- **File**: `%s`\n", res.File)
	}
	fmt.Fprintf(&sb, "- **Applied to Disk**: `%v`\n", res.Applied)
	if len(res.Added) > 0 {
		fmt.Fprintf(&sb, "- **Added Symbols**: %s\n\n", strings.Join(res.Added, ", "))
	}

	if res.Diff != "" {
		sb.WriteString("### Proposed AST Diff\n```diff\n")
		sb.WriteString(res.Diff)
		sb.WriteString("```\n")
	} else {
		sb.WriteString("\n*No modifications needed (declarations already satisfy contract).*\n")
	}

	return sb.String(), nil
}
