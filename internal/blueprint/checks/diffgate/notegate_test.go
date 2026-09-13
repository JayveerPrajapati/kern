package diffgate

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/blueprint/domain"
	"github.com/JayveerPrajapati/kern/internal/note"
)

func writeNoteTree(t *testing.T, root string, valid bool) {
	t.Helper()
	rel := "docs/notes/implemented/architecture/2026-09-11-example.md"
	content := note.Skeleton(note.Implemented, "Example", "example decision\n")
	if !valid {
		content = "# Agent Note: Example\nStatus: proposed\n\n## Problem\nx\n"
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestG37_NoteFormatValid(t *testing.T) {
	root := t.TempDir()
	writeNoteTree(t, root, true)
	c := NewNoteFormatCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("valid note tree should PASS, got %s (%s)", res.Status, res.Error)
	}
}

func TestG37_NoteFormatViolation(t *testing.T) {
	root := t.TempDir()
	writeNoteTree(t, root, false)
	c := NewNoteFormatCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusBlock {
		t.Fatalf("violating note tree should BLOCK, got %s", res.Status)
	}
	found := false
	for _, f := range res.Findings {
		if strings.Contains(f.Message, "does not agree") {
			found = true
		}
	}
	if !found {
		t.Errorf("expected a status/lifecycle violation finding, got %v", res.Findings)
	}
}

func TestG37_NoteFormatNoTree(t *testing.T) {
	root := t.TempDir()
	c := NewNoteFormatCheck(root)
	res, err := c.Run(context.Background(), domain.ChangeRequest{})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("absent note tree should PASS (no notes to validate), got %s", res.Status)
	}
}

func TestG38_NoteMissingWarns(t *testing.T) {
	c := NewNoteMissingCheck()
	res, err := c.Run(context.Background(), domain.ChangeRequest{
		Files: []domain.FileChange{
			{Path: "internal/context/orchestrate.go", Op: domain.OpWrite},
		},
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusWarn {
		t.Fatalf("source change without note should WARN, got %s", res.Status)
	}
}

func TestG38_NoteMissingPassesWithNote(t *testing.T) {
	c := NewNoteMissingCheck()
	res, err := c.Run(context.Background(), domain.ChangeRequest{
		Files: []domain.FileChange{
			{Path: "internal/context/orchestrate.go", Op: domain.OpWrite},
			{Path: "docs/notes/implemented/feature/2026-09-11-x.md", Op: domain.OpWrite},
		},
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("source change with note should PASS, got %s", res.Status)
	}
}

func TestG38_NoteMissingPassesDocOnly(t *testing.T) {
	c := NewNoteMissingCheck()
	res, err := c.Run(context.Background(), domain.ChangeRequest{
		Files: []domain.FileChange{
			{Path: "docs/context-envelope.md", Op: domain.OpWrite},
		},
	})
	if err != nil {
		t.Fatalf("Run error: %v", err)
	}
	if res.Status != domain.StatusPass {
		t.Fatalf("doc-only change should PASS, got %s", res.Status)
	}
}