package health

import (
	"context"
	"strings"
	"testing"
)

func TestHealthSnapshot(t *testing.T) {
	ctx := context.Background()
	info := ServerInfo{
		Transport:       "stdio",
		Version:         "test",
		Protocol:        "2024-11-05",
		Roots:           []string{"/test"},
		ToolsRegistered: 10,
		ToolsAdvertised: 5,
	}
	res, err := Health(ctx, info, map[string]any{})
	if err != nil {
		t.Fatalf("Health failed: %v", err)
	}
	if !strings.Contains(res, `"status": "ok"`) {
		t.Errorf("expected status ok in json, got: %s", res)
	}
}
