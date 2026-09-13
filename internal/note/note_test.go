package note

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeNote(t *testing.T, root, rel, content string) string {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	return abs
}

func TestSlug(t *testing.T) {
	cases := map[string]string{
		"Silent Orchestrator Pipeline": "silent-orchestrator-pipeline",
		"Fix: HTTP Timeout (v2)":       "fix-http-timeout-v2",
		"####":                         "",
		"a":                            "a",
	}
	for in, want := range cases {
		if got := Slug(in); got != want {
			t.Errorf("Slug(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestPathFor(t *testing.T) {
	p, err := PathFor(Proposed, Architecture, "2026-09-11", "Move Notes Out of Repo")
	if err != nil {
		t.Fatal(err)
	}
	want := "docs/notes/proposed/architecture/2026-09-11-move-notes-out-of-repo.md"
	if p != want {
		t.Errorf("PathFor = %q, want %q", p, want)
	}
	if _, err := PathFor("bogus", Architecture, "2026-09-11", "x"); err == nil {
		t.Error("expected error for bogus lifecycle")
	}
	if _, err := PathFor(Proposed, "bogus", "2026-09-11", "x"); err == nil {
		t.Error("expected error for bogus class")
	}
	if _, err := PathFor(Proposed, Architecture, "2026-13-99", "x"); err == nil {
		t.Error("expected error for bad date")
	}
	if _, err := PathFor(Proposed, Architecture, "2026-09-11", "###"); err == nil {
		t.Error("expected error for empty slug")
	}
}

func TestParseFileValid(t *testing.T) {
	root := t.TempDir()
	rel := "docs/notes/implemented/architecture/2026-09-11-silent-orchestration.md"
	abs := writeNote(t, root, rel, Skeleton(Implemented, "Silent Orchestration", "kern_orchestrate ships the silent context pipeline.\n\n## Decision\nDone.\n"))
	n, v := ParseFile(abs)
	if len(v) != 0 {
		t.Fatalf("valid note produced violations: %v", v)
	}
	if n.Title != "Silent Orchestration" || n.Lifecycle != Implemented || n.Class != Architecture {
		t.Errorf("parsed note mismatch: %+v", n)
	}
}

func TestParseFileStatusLifecycleMismatch(t *testing.T) {
	root := t.TempDir()
	rel := "docs/notes/implemented/architecture/2026-09-11-x.md"
	content := "# Agent Note: X\nStatus: proposed\n\n## Problem\nx\n"
	abs := writeNote(t, root, rel, content)
	_, v := ParseFile(abs)
	if len(v) == 0 {
		t.Fatal("expected violation for status/lifecycle mismatch")
	}
	ok := false
	for _, vi := range v {
		if strings.Contains(vi.Message, "does not agree") {
			ok = true
		}
	}
	if !ok {
		t.Errorf("expected agree-violation, got %v", v)
	}
}

func TestParseFileRejectedRequiresReason(t *testing.T) {
	root := t.TempDir()
	rel := "docs/notes/rejected/process/2026-09-11-x.md"
	content := "# Agent Note: X\nStatus: rejected\n\n## Problem\nx\n"
	abs := writeNote(t, root, rel, content)
	_, v := ParseFile(abs)
	ok := false
	for _, vi := range v {
		if strings.Contains(vi.Message, "rejected — ") {
			ok = true
		}
	}
	if !ok {
		t.Errorf("expected rejected-reason violation, got %v", v)
	}
}

func TestParseFileArchivedRequiresArchiveLine(t *testing.T) {
	root := t.TempDir()
	rel := "docs/notes/archived/architecture/2026-09-11-x.md"
	content := "# Agent Note: X\nStatus: implemented\n\n## Problem\nx\n"
	abs := writeNote(t, root, rel, content)
	_, v := ParseFile(abs)
	ok := false
	for _, vi := range v {
		if strings.Contains(vi.Message, "Archived:") {
			ok = true
		}
	}
	if !ok {
		t.Errorf("expected Archived-line violation, got %v", v)
	}
}

func TestParseFileMissingProblem(t *testing.T) {
	root := t.TempDir()
	rel := "docs/notes/proposed/feature/2026-09-11-x.md"
	content := "# Agent Note: X\nStatus: proposed\n\nSome body without Problem.\n"
	abs := writeNote(t, root, rel, content)
	_, v := ParseFile(abs)
	ok := false
	for _, vi := range v {
		if strings.Contains(vi.Message, "## Problem") {
			ok = true
		}
	}
	if !ok {
		t.Errorf("expected ## Problem violation, got %v", v)
	}
}

func TestParseFileBadFilename(t *testing.T) {
	root := t.TempDir()
	rel := "docs/notes/proposed/feature/not-a-date.md"
	abs := writeNote(t, root, rel, Skeleton(Proposed, "X", "x\n"))
	_, v := ParseFile(abs)
	ok := false
	for _, vi := range v {
		if strings.Contains(vi.Message, "filename must be") {
			ok = true
		}
	}
	if !ok {
		t.Errorf("expected filename violation, got %v", v)
	}
}

func TestValidateTreeEmpty(t *testing.T) {
	root := t.TempDir()
	v, err := ValidateTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 0 {
		t.Errorf("empty tree should have no violations: %v", v)
	}
}

func TestValidateTreeAggregates(t *testing.T) {
	root := t.TempDir()
	writeNote(t, root, "docs/notes/implemented/architecture/2026-09-11-good.md", Skeleton(Implemented, "Good", "fine\n"))
	writeNote(t, root, "docs/notes/implemented/feature/2026-09-11-bad.md", "# Agent Note: Bad\nStatus: proposed\n\n## Problem\nx\n")
	v, err := ValidateTree(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) == 0 {
		t.Fatal("expected violations from the bad note")
	}
}
func TestParseFileRejectedWithReasonValid(t *testing.T) {
	root := t.TempDir()
	rel := "docs/notes/rejected/process/2026-09-11-x.md"
	content := "# Agent Note: X\nStatus: rejected — not needed\n\n## Problem\nx\n"
	abs := writeNote(t, root, rel, content)
	_, v := ParseFile(abs)
	for _, vi := range v {
		if strings.Contains(vi.Message, "does not agree") || strings.Contains(vi.Message, "rejected — ") {
			t.Errorf("valid rejected note flagged: %v", vi)
		}
	}
}

func TestParseFileArchivedValid(t *testing.T) {
	root := t.TempDir()
	rel := "docs/notes/archived/feature/2026-09-11-x.md"
	content := "# Agent Note: X\nStatus: implemented\n\nArchived: 2026-09-11\n## Problem\nx\n"
	abs := writeNote(t, root, rel, content)
	_, v := ParseFile(abs)
	if len(v) != 0 {
		t.Fatalf("valid archived note flagged: %v", v)
	}
}
