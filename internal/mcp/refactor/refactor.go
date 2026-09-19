// Package refactor owns multi-file refactoring transaction MCP tool bodies
// (kern_refactor_transaction) as plain functions.
package refactor

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/refactor"
)

// Hooks provides session invalidation hooks from the owning MCP server.
type Hooks struct {
	InvalidateSession func(root string)
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

// Transaction executes atomic multi-file refactoring transactions with rollback safety.
func Transaction(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	root := resolveRoot(mcpargs.ArgString(args, "root"))

	rawEdits, ok := args["edits"]
	if !ok || rawEdits == nil {
		return "", fmt.Errorf("edits parameter is required")
	}

	var edits []refactor.FileEdit
	switch val := rawEdits.(type) {
	case string:
		if err := json.Unmarshal([]byte(val), &edits); err != nil {
			return "", fmt.Errorf("parse edits JSON string: %w", err)
		}
	case []any:
		data, err := json.Marshal(val)
		if err != nil {
			return "", fmt.Errorf("marshal edits: %w", err)
		}
		if err := json.Unmarshal(data, &edits); err != nil {
			return "", fmt.Errorf("decode edits: %w", err)
		}
	case []refactor.FileEdit:
		edits = val
	default:
		return "", fmt.Errorf("unsupported edits format: %T", rawEdits)
	}

	absRoot, err := filepath.Abs(root)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	for i := range edits {
		if strings.TrimSpace(edits[i].Path) == "" {
			return "", fmt.Errorf("edits[%d].path is required (each edit is {path, content} with the new complete file content)", i)
		}
		rel := filepath.Clean(edits[i].Path)
		if filepath.IsAbs(rel) {
			r, err := filepath.Rel(absRoot, rel)
			if err != nil || strings.HasPrefix(r, "..") {
				return "", fmt.Errorf("edits[%d].path %q is outside the project root", i, edits[i].Path)
			}
			rel = r
		} else if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("edits[%d].path %q escapes the project root", i, edits[i].Path)
		}
		edits[i].Path = rel
	}

	compileCmd := mcpargs.ArgString(args, "compile_command")
	apply := mcpargs.ArgBool(args, "apply")

	res, err := refactor.ExecuteTransaction(ctx, refactor.TransactionRequest{
		Root:           root,
		Edits:          edits,
		CompileCommand: compileCmd,
		Apply:          apply,
	})
	if err != nil {
		return "", fmt.Errorf("transaction execution error: %w", err)
	}

	if mcpargs.ArgString(args, "format") == "json" {
		data, _ := json.MarshalIndent(res, "", "  ")
		return string(data), nil
	}

	if !res.Success {
		return fmt.Sprintf("❌ Transaction FAILED & ROLLED BACK (0 files modified on disk)\n\nError: %s\n\nCompiler Output:\n%s\n\nAttempted Diff:\n%s",
			res.Error, res.CompilerOutput, res.UnifiedDiff), nil
	}

	actionWord := "Previewed"
	if apply {
		actionWord = "Committed to live disk"
		if h.InvalidateSession != nil {
			h.InvalidateSession(root)
		}
	}

	return fmt.Sprintf("✅ Transaction SUCCESSFUL (%s %d files)\n\nModified files: %v\n\nUnified Diff:\n%s",
		actionWord, len(res.ModifiedFiles), res.ModifiedFiles, res.UnifiedDiff), nil
}
