package mcp

import (
	"context"
	"io"
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