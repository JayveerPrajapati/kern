package mcp

import (
	"context"
	"strings"
	"testing"
)

func TestHandleMutationTest_DryRun(t *testing.T) {
	s := NewServer(strings.NewReader(""), nil)
	defer s.Close()

	res, err := s.handleMutationTest(context.Background(), map[string]any{
		"root":    ".",
		"dry_run": "true",
	})
	if err != nil {
		t.Fatalf("handleMutationTest error: %v", err)
	}

	if !strings.Contains(res, "Mutation Testing Report") {
		t.Errorf("expected report header, got: %s", res)
	}
}
