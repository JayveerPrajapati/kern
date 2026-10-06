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

// TestHandleMemoryAddWritesTypedStore pins the P2-14 follow-up for the MCP
// surface: kern_memory action=add must write the TYPED store (buddy's
// "Project memory" source) with a non-auto Source so the digest renders it,
// while still writing the v1 store so recall keeps working.
func TestHandleMemoryAddWritesTypedStore(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	srv := newTestServer()
	ctx := context.Background()
	root := t.TempDir()

	res, err := srv.handleMemory(ctx, map[string]any{
		"action": "add",
		"lesson": "Never commit plain-text API secrets to git repository",
		"root":   root,
	})
	if err != nil {
		t.Fatalf("handleMemory action=add failed: %v", err)
	}
	if !strings.Contains(res, "remembered.") {
		t.Fatalf("unexpected add response: %q", res)
	}

	// Typed store: the lesson must be current and not auto-sourced, so
	// buddy's "Project memory" renders it.
	mems, err := memory.NewMemoryStore(root).CurrentMemories("")
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, m := range mems {
		if m.Content == "Never commit plain-text API secrets to git repository" {
			found = true
			if m.Source == "auto" {
				t.Fatalf("explicit lesson Source = %q, want non-auto", m.Source)
			}
		}
	}
	if !found {
		t.Fatalf("action=add lesson missing from typed store: %+v", mems)
	}

	// v1 store: recall still finds it.
	if got := memory.Recall(root, "commit api secrets to git", memory.DefaultRecallLimit); len(got) == 0 {
		t.Fatal("action=add lesson missing from v1 recall")
	}
}
