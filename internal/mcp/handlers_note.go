package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/note"
)

// handleNote implements kern_note: the model-facing face of the governed
// decision-record system (docs/notes/{lifecycle}/{class}/yyyy-mm-dd-title.md).
// Actions: new (create a gate-conformant skeleton), status (move between
// lifecycle folders — rejected needs a reason, archived inserts the frozen
// marker), validate (report format violations), list (inventory). Mirrors
// the kern note CLI so agents can satisfy the note:missing gate (G38) from
// the tool surface.
func (s *Server) handleNote(ctx context.Context, args map[string]any) (string, error) {
	action := strings.ToLower(argString(args, "action"))
	if action == "" {
		action = "list"
	}
	root := resolveRoot(argString(args, "root"))

	switch action {
	case "new":
		title := strings.TrimSpace(argString(args, "title"))
		if title == "" {
			return "", fmt.Errorf("title is required for action=new")
		}
		lifecycle := note.Lifecycle(argString(args, "lifecycle"))
		if lifecycle == "" {
			lifecycle = note.Proposed
		}
		class := note.Class(argString(args, "class"))
		if class == "" {
			return "", fmt.Errorf("class is required for action=new (feature|bug-fix|simplification|architecture|process|testing)")
		}
		date := argString(args, "date")
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
		file := argString(args, "file")
		if file == "" {
			return "", fmt.Errorf("file is required for action=status")
		}
		target := note.Lifecycle(argString(args, "set"))
		if target == "" {
			return "", fmt.Errorf("set (target lifecycle) is required for action=status")
		}
		newRel, err := note.Move(root, file, target, argString(args, "reason"), time.Now())
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
