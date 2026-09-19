package skill

import (
	"context"
	"strings"
	"testing"
)

func TestSkillCatalog(t *testing.T) {
	ctx := context.Background()
	res, err := Skill(ctx, map[string]any{
		"action": "catalog",
	})
	if err != nil {
		t.Fatalf("Skill catalog failed: %v", err)
	}
	if !strings.Contains(res, "name") {
		t.Errorf("expected skill catalog output, got: %s", res)
	}
}

func TestSkillUnknownAction(t *testing.T) {
	ctx := context.Background()
	_, err := Skill(ctx, map[string]any{
		"action": "unknown_action_xyz",
	})
	if err == nil || !strings.Contains(err.Error(), "unknown action") {
		t.Fatalf("expected error on unknown action, got: %v", err)
	}
}
