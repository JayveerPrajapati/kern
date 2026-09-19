// Package synthtest owns the test synthesis MCP tool bodies (kern_synthesize_test)
// as plain functions.
package synthtest

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/synthtest"
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

// Synthesize scaffolds table-driven tests and edge-case invariants for symbols.
func Synthesize(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := resolveRoot(mcpargs.ArgString(args, "root"))
	target := mcpargs.ArgString(args, "target")
	file := mcpargs.ArgString(args, "file")
	code := mcpargs.ArgString(args, "code")
	autoGap := mcpargs.ArgBool(args, "auto_gap")
	apply := mcpargs.ArgBool(args, "apply")
	format := mcpargs.ArgString(args, "format")

	var ix *index.Index
	if autoGap && h.LoadIndex != nil {
		ix, _ = h.LoadIndex(ctx, root)
	}

	req := synthtest.Request{
		Target:  target,
		File:    file,
		Code:    code,
		Root:    root,
		AutoGap: autoGap,
		Apply:   apply,
		Index:   ix,
	}

	res, err := synthtest.Synthesize(req)
	if err != nil {
		return "", fmt.Errorf("synthesize test: %w", err)
	}

	if format == "json" {
		b, jerr := json.MarshalIndent(res, "", "  ")
		if jerr != nil {
			return "", jerr
		}
		return string(b), nil
	}

	var sb strings.Builder
	sb.WriteString("# Synthesize Test Report\n\n")
	sb.WriteString(fmt.Sprintf("- **Target Symbol**: `%s`\n", res.TargetSymbol))
	if res.TargetFile != "" {
		sb.WriteString(fmt.Sprintf("- **Target Source**: `%s`\n", res.TargetFile))
	}
	sb.WriteString(fmt.Sprintf("- **Test File**: `%s`\n", res.TestFile))
	sb.WriteString(fmt.Sprintf("- **Test Function**: `%s`\n", res.TestFunction))
	sb.WriteString(fmt.Sprintf("- **Applied to Disk**: `%v`\n", res.Applied))
	if res.Message != "" {
		sb.WriteString(fmt.Sprintf("- **Notice**: %s\n", res.Message))
	}
	if len(res.Cases) > 0 {
		sb.WriteString(fmt.Sprintf("- **Synthesized Cases**: %s\n\n", strings.Join(res.Cases, ", ")))
	}

	if res.Diff != "" {
		sb.WriteString("### Proposed Test Diff\n```diff\n")
		sb.WriteString(res.Diff)
		sb.WriteString("```\n\n")
	}

	sb.WriteString("### Synthesized Test Code\n```go\n")
	sb.WriteString(res.TestCode)
	sb.WriteString("\n```\n")

	return sb.String(), nil
}
