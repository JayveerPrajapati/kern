// Package note owns the governed decision-record MCP tool bodies (kern_note)
// as plain functions.
package note

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/mcp/mcpargs"
	"github.com/JayveerPrajapati/kern/internal/note"
)

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

// Handle implements kern_note: the model-facing face of the governed
// decision-record system (docs/notes/{lifecycle}/{class}/yyyy-mm-dd-title.md).
// Actions: new (create a gate-conformant skeleton), status (move between
// lifecycle folders — rejected needs a reason, archived inserts the frozen
// marker), validate (report format violations), list (inventory).
func Handle(ctx context.Context, args map[string]any) (string, error) {
	action := strings.ToLower(mcpargs.ArgString(args, "action"))
	if action == "" {
		action = "list"
	}
	root := resolveRoot(mcpargs.ArgString(args, "root"))

	switch action {
	case "new":
		title := strings.TrimSpace(mcpargs.ArgString(args, "title"))
		if title == "" {
			return "", fmt.Errorf("title is required for action=new")
		}
		lifecycle := note.Lifecycle(mcpargs.ArgString(args, "lifecycle"))
		if lifecycle == "" {
			lifecycle = note.Proposed
		}
		class := note.Class(mcpargs.ArgString(args, "class"))
		if class == "" {
			return "", fmt.Errorf("class is required for action=new (feature|bug-fix|simplification|architecture|process|testing)")
		}
		date := mcpargs.ArgString(args, "date")
		if date == "" {
			date = time.Now().Format("2006-01-02")
		}
		rel, err := note.PathFor(lifecycle, class, date, title)
		if err != nil {
			return "", err
		}
		abs := filepath.Join(root, filepath.FromSlash(rel))
		if _, err := os.Stat(abs); err == nil {
			return "", fmt.Errorf("note already exists at %s", rel)
		}
		if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
			return "", err
		}
		body := fmt.Sprintf("Record the decision for this %s change. Describe the problem, what was decided, and what was given up.", class)
		if err := os.WriteFile(abs, []byte(note.Skeleton(lifecycle, title, body)), 0o644); err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(map[string]any{"status": "created", "path": rel}, "", "  ")
		return string(out), nil

	case "status":
		file := mcpargs.ArgString(args, "file")
		if file == "" {
			return "", fmt.Errorf("file is required for action=status")
		}
		target := note.Lifecycle(mcpargs.ArgString(args, "set"))
		if target == "" {
			return "", fmt.Errorf("set (target lifecycle) is required for action=status")
		}
		newRel, err := note.Move(root, file, target, mcpargs.ArgString(args, "reason"), time.Now())
		if err != nil {
			return "", err
		}
		out, _ := json.MarshalIndent(map[string]any{"status": "moved", "from": file, "to": newRel}, "", "  ")
		return string(out), nil

	case "validate":
		violations, err := note.ValidateTree(root)
		if err != nil {
			return "", err
		}
		if len(violations) == 0 {
			return `{"status":"valid","violations":0}`, nil
		}
		items := make([]map[string]string, 0, len(violations))
		for _, v := range violations {
			items = append(items, map[string]string{"path": v.Path, "message": v.Message})
		}
		out, _ := json.MarshalIndent(map[string]any{"status": "invalid", "violations": items}, "", "  ")
		return string(out), nil

	case "list":
		paths, err := note.WalkNotes(root)
		if err != nil {
			return "", err
		}
		entries := make([]string, 0, len(paths))
		for _, p := range paths {
			rel, _ := filepath.Rel(root, p)
			entries = append(entries, filepath.ToSlash(rel))
		}
		out, _ := json.MarshalIndent(map[string]any{"count": len(entries), "notes": entries}, "", "  ")
		return string(out), nil

	default:
		return "", fmt.Errorf("unknown action %q (use new|status|validate|list)", action)
	}
}
