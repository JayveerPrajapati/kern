package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

func TestHandleMemoryRanked(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()
	tmpDir := t.TempDir()

	_ = memory.Add(tmpDir, "Always run tests before committing code changes")
	_ = memory.Add(tmpDir, "Never commit plain-text API secrets to git repository")

	res, err := srv.handleMemoryRanked(ctx, map[string]any{
		"prompt":         "How should we handle secrets and api keys?",
		"k":              5,
		"half_life_days": 7.0,
		"root":           tmpDir,
	})
	if err != nil {
		t.Fatalf("handleMemoryRanked failed: %v", err)
	}

	if !strings.Contains(res, "Decay-Ranked Memory Retrieval") {
		t.Errorf("expected Decay-Ranked Memory Retrieval header, got: %s", res)
	}
	if !strings.Contains(res, "secrets") {
		t.Errorf("expected secrets lesson in results, got: %s", res)
	}
}
