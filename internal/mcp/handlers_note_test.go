package mcp

import (
	"context"
	"encoding/json"
	"io"
	"strings"
	"testing"
)

func TestNoteList(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	root := t.TempDir()
	s.roots = []string{root}
	res, err := s.handleNote(context.Background(), map[string]any{"action": "list", "root": root})
	if err != nil {
		t.Fatalf("list error: %v", err)
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(res), &parsed); err != nil {
		t.Fatal(err)
	}
	if parsed["count"].(float64) != 0 {
		t.Errorf("expected empty tree, got %v", parsed["count"])
	}
}

func TestNoteNewThenValidate(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	root := t.TempDir()
	s.roots = []string{root}

	// Create an implemented architecture note via the tool.
	res, err := s.handleNote(context.Background(), map[string]any{
		"action":    "new",
		"title":     "Example Decision",
		"class":     "architecture",
		"lifecycle": "implemented",
		"date":      "2026-09-11",
		"root":      root,
	})
	if err != nil {
		t.Fatalf("new error: %v", err)
	}
	var created map[string]any
	_ = json.Unmarshal([]byte(res), &created)
	path, _ := created["path"].(string)
	if !strings.Contains(path, "implemented/architecture/2026-09-11-example-decision.md") {
		t.Errorf("unexpected created path: %s", path)
	}

	// The tree must validate (the skeleton is gate-conformant).
	vres, err := s.handleNote(context.Background(), map[string]any{"action": "validate", "root": root})
	if err != nil {
		t.Fatalf("validate error: %v", err)
	}
	if !strings.Contains(vres, `"valid"`) {
		t.Errorf("expected valid tree, got %s", vres)
	}

	// Move it to rejected with a reason.
	sres, err := s.handleNote(context.Background(), map[string]any{
		"action": "status",
		"file":   path,
		"set":    "rejected",
		"reason": "not needed",
		"root":   root,
	})
	if err != nil {
		t.Fatalf("status error: %v", err)
	}
	if !strings.Contains(sres, "rejected") {
		t.Errorf("expected move to rejected, got %s", sres)
	}
}

func TestNoteNewMissingFields(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	s.roots = []string{t.TempDir()}
	if _, err := s.handleNote(context.Background(), map[string]any{"action": "new", "root": s.roots[0]}); err == nil {
		t.Fatal("expected error when title missing")
	}
	if _, err := s.handleNote(context.Background(), map[string]any{"action": "new", "title": "x", "root": s.roots[0]}); err == nil {
		t.Fatal("expected error when class missing")
	}
}

func TestNoteUnknownAction(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	_, err := s.handleNote(context.Background(), map[string]any{"action": "explode", "root": t.TempDir()})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}
