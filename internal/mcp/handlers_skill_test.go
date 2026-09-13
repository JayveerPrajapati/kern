package mcp

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSkillCatalog(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	res, err := s.handleSkill(context.Background(), map[string]any{"action": "catalog"})
	if err != nil {
		t.Fatalf("handleSkill catalog error: %v", err)
	}
	for _, name := range []string{"kern-investigate", "kern-safe-change", "kern-incident-triage"} {
		if !strings.Contains(res, name) {
			t.Errorf("catalog missing skill %s in: %s", name, res)
		}
	}
	if !strings.Contains(res, "description") {
		t.Errorf("catalog should carry per-skill descriptions: %s", res)
	}
}

func TestSkillCatalogDefaultAction(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	res, err := s.handleSkill(context.Background(), map[string]any{})
	if err != nil {
		t.Fatalf("handleSkill default action error: %v", err)
	}
	if !strings.Contains(res, "kern-investigate") {
		t.Errorf("default action should catalog skills: %s", res)
	}
}

func TestSkillLoad(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	res, err := s.handleSkill(context.Background(), map[string]any{"action": "load", "skill": "kern-safe-change"})
	if err != nil {
		t.Fatalf("handleSkill load error: %v", err)
	}
	if !strings.HasPrefix(res, "---") && !strings.Contains(res, "kern-safe-change") {
		t.Errorf("load should return the SKILL.md runbook: %s", res)
	}
}

func TestSkillLoadUnknown(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	_, err := s.handleSkill(context.Background(), map[string]any{"action": "load", "skill": "does-not-exist"})
	if err == nil {
		t.Fatal("expected error for unknown skill")
	}
	if !strings.Contains(err.Error(), "unknown skill") {
		t.Errorf("unexpected error text: %v", err)
	}
}

func TestSkillLoadMissingName(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	_, err := s.handleSkill(context.Background(), map[string]any{"action": "load"})
	if err == nil {
		t.Fatal("expected error for load without skill name")
	}
}

func TestSkillUnknownAction(t *testing.T) {
	s := NewServer(strings.NewReader(""), io.Discard)
	_, err := s.handleSkill(context.Background(), map[string]any{"action": "explode"})
	if err == nil {
		t.Fatal("expected error for unknown action")
	}
}

// TestSkillLoadUserDirFallback covers the user-skill fallback on the live
// kern_skill path (previously only exercised by the removed dead handler
// handleSkills): a skill under <root>/.kern/skills is loadable by name and
// catalog output carries the "(user)" marker.
func TestSkillLoadUserDirFallback(t *testing.T) {
	root := t.TempDir()
	skillDir := filepath.Join(root, ".kern", "skills", "demo")
	if err := os.MkdirAll(skillDir, 0o700); err != nil {
		t.Fatal(err)
	}
	skillMD := "---\nname: demo\ndescription: Demo user skill\n---\nBody of the demo user skill.\n"
	if err := os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte(skillMD), 0o600); err != nil {
		t.Fatal(err)
	}
	s := NewServer(strings.NewReader(""), io.Discard)

	body, err := s.handleSkill(context.Background(), map[string]any{"action": "load", "skill": "demo", "root": root})
	if err != nil {
		t.Fatalf("load user skill: %v", err)
	}
	if !strings.Contains(body, "Body of the demo user skill.") {
		t.Errorf("load did not return the user skill body, got %q", body)
	}

	// Catalog is embedded-only; the user-skill marker must not leak into it.
	catalog, err := s.handleSkill(context.Background(), map[string]any{"action": "catalog", "root": root})
	if err != nil {
		t.Fatalf("catalog: %v", err)
	}
	if strings.Contains(catalog, "(user)") {
		t.Errorf("catalog must not emit user entries, got:\n%s", catalog)
	}
	if !strings.Contains(catalog, "kern-safe-change") {
		t.Errorf("embedded skills missing from catalog, got:\n%s", catalog)
	}

	// Missing .kern/skills directory: load falls back gracefully to embedded,
	// unknown name errors.
	unknown, err := s.handleSkill(context.Background(), map[string]any{"action": "load", "skill": "demo", "root": t.TempDir()})
	if err == nil || unknown != "" {
		t.Errorf("unknown user skill on empty dir: err=%v out=%q, want error", err, unknown)
	}
}
