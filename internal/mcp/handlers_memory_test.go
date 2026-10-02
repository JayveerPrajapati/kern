package mcp

import (
	"context"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/memory"
)

// TestHandleMemoryRanked covers the consolidated kern_memory tool's
// action=ranked body (decay-weighted retrieval, markdown rendering).
func TestHandleMemoryRanked(t *testing.T) {
	t.Parallel()
	srv := newTestServer()
	ctx := context.Background()
	tmpDir := t.TempDir()

	_ = memory.Add(tmpDir, "Always run tests before committing code changes")
	_ = memory.Add(tmpDir, "Never commit plain-text API secrets to git repository")

	res, err := srv.handleMemory(ctx, map[string]any{
		"action":         "ranked",
		"prompt":         "How should we handle secrets and api keys?",
		"k":              5,
		"half_life_days": 7.0,
		"root":           tmpDir,
	})
	if err != nil {
		t.Fatalf("handleMemory action=ranked failed: %v", err)
	}

	if !strings.Contains(res, "Decay-Ranked Memory Retrieval") {
		t.Errorf("expected Decay-Ranked Memory Retrieval header, got: %s", res)
	}
	if !strings.Contains(res, "secrets") {
		t.Errorf("expected secrets lesson in results, got: %s", res)
	}
}
