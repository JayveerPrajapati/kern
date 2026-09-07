package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/transform"
)

// handleAstTransform executes an AST-level semantic mutation (e.g. scaffolding
// interface implementations, adding struct fields, or inserting methods) without
// fragile whitespace or regex diff replacements.
func (s *Server) handleAstTransform(ctx context.Context, args map[string]any) (string, error) {
	root := resolveRoot(argString(args, "root"))
	action := argString(args, "action")
	if action == "" {
		action = "implement_interface"
	}

	var ix *index.Index
	if argString(args, "code") == "" {
		ix, _ = s.loadIndex(ctx, root)
	}

	req := transform.Request{
		Action:          action,
		File:            argString(args, "file"),
		Code:            argString(args, "code"),
		Root:            root,
		TargetSymbol:    argString(args, "target_symbol"),
		InterfaceName:   argString(args, "interface_name"),
		ReceiverName:    argString(args, "receiver_name"),
		ReceiverType:    argString(args, "receiver_type"),
		FieldName:       argString(args, "field_name"),
		FieldType:       argString(args, "field_type"),
		FieldTag:        argString(args, "field_tag"),
		MethodSignature: argString(args, "method_signature"),
		MethodBody:      argString(args, "method_body"),
		Apply:           argBool(args, "apply"),
		Index:           ix,
	}

	res, err := transform.Transform(req)
	if err != nil {
		return "", fmt.Errorf("ast transform: %w", err)
	}

	if argString(args, "format") == "json" {
		b, jerr := json.MarshalIndent(res, "", "  ")
		if jerr != nil {
			return "", jerr
		}
		return string(b), nil
	}

	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("# AST Transform Report: %s\n\n", res.Action))
	sb.WriteString(fmt.Sprintf("- **Target Symbol**: `%s`\n", res.TargetSymbol))
	if res.File != "" {
		sb.WriteString(fmt.Sprintf("- **File**: `%s`\n", res.File))
	}
	sb.WriteString(fmt.Sprintf("- **Applied to Disk**: `%v`\n", res.Applied))
	if len(res.Added) > 0 {
		sb.WriteString(fmt.Sprintf("- **Added Symbols**: %s\n\n", strings.Join(res.Added, ", ")))
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
