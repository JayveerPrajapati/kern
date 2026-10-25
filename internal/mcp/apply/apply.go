// Package apply owns the kern_apply MCP tool body: the one-command safe
// edit path over internal/apply (transactional apply -> scoped verify ->
// rollback on failure), with the MCP-side argument shaping and containment
// validation mirrored from internal/mcp/refactor.
package apply

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/apply"
	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/mcp/root"
	"github.com/JayveerPrajapati/kern/internal/refactor"
)

// Hooks provides session invalidation hooks from the owning MCP server.
type Hooks struct {
	InvalidateSession func(root string)
}

// Apply executes kern_apply: apply edits transactionally, verify the
// result, roll back the live tree on verify failure.
func Apply(ctx context.Context, h Hooks, args map[string]any) (string, error) {
	r := root.ResolveRoot(mcpargs.ArgString(args, "root"))

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

	absRoot, err := filepath.Abs(r)
	if err != nil {
		return "", fmt.Errorf("resolve root: %w", err)
	}
	for i := range edits {
		if strings.TrimSpace(edits[i].Path) == "" {
			return "", fmt.Errorf("edits[%d].path is required (each edit is {path, content} with the new complete file content)", i)
		}
		rel := filepath.Clean(edits[i].Path)
		if filepath.IsAbs(rel) {
			r2, err := filepath.Rel(absRoot, rel)
			if err != nil || strings.HasPrefix(r2, "..") {
				return "", fmt.Errorf("edits[%d].path %q is outside the project root", i, edits[i].Path)
			}
			rel = r2
		} else if rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
			return "", fmt.Errorf("edits[%d].path %q escapes the project root", i, edits[i].Path)
		}
		edits[i].Path = rel
	}

	// Security: unlike kern_refactor_transaction (client-supplied
	// compile_command) and kern_mutate_test (client-supplied test_command),
	// kern_apply has NO client-supplied command string — the only exec the
	// request can trigger is the engine's derived default (`go test` on the
	// git-scoped package set when test=true), a fixed-argv, no-shell
	// invocation under fsutil.MinimalExecEnv with paths confined to the root.
	// That is the same carve-out the sibling tools document for their
	// engine-derived defaults (refactor.go / mutation.go): it is not gated by
	// CheckExecCommand, only a client-supplied command would be. The
	// unsuppressed unsafe-exec finding at internal/apply/apply.go:233 is the
	// same class and is triaged in defaultSuppressions.
	res, err := apply.Run(ctx, apply.Request{
		Root:   r,
		Edits:  edits,
		Test:   mcpargs.ArgBool(args, "test"),
		DryRun: mcpargs.ArgBool(args, "dry_run"),
	})
	if err != nil {
		return "", fmt.Errorf("apply execution error: %w", err)
	}

	if h.InvalidateSession != nil && res.Applied {
		h.InvalidateSession(r)
	}

	if mcpargs.ArgString(args, "format") == "json" {
		data, _ := json.MarshalIndent(res, "", "  ")
		return string(data), nil
	}
	return render(res), nil
}

func render(res *apply.Result) string {
	var b strings.Builder
	if res.RolledBack {
		fmt.Fprintf(&b, "❌ kern apply FAILED & ROLLED BACK (live tree restored)\n\nError: %s\n\n", res.Error)
	} else if !res.Success {
		fmt.Fprintf(&b, "❌ kern apply FAILED (live tree untouched)\n\nError: %s\n\n", res.Error)
	} else if res.Applied {
		fmt.Fprintf(&b, "✅ kern apply APPLIED & VERIFIED\n\n")
	} else {
		fmt.Fprintf(&b, "✅ kern apply PREVIEWED (dry-run — tree untouched)\n\n")
	}
	fmt.Fprintf(&b, "Modified files: %s\n", strings.Join(res.ModifiedFiles, ", "))
	if res.UnifiedDiff != "" {
		fmt.Fprintf(&b, "\n%s\n", res.UnifiedDiff)
	}
	if res.BuildOutput != "" {
		fmt.Fprintf(&b, "\nBuild output:\n%s\n", res.BuildOutput)
	}
	if res.TestOutput != "" {
		fmt.Fprintf(&b, "\nTest output:\n%s\n", res.TestOutput)
	}
	fmt.Fprintf(&b, "\nreceipt: %s\n", res.Receipt)
	return b.String()
}
