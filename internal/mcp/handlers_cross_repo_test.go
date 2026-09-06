package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleCrossRepoImpact(t *testing.T) {
	srv := newTestServer()
	ctx := context.Background()

	tmpDir := t.TempDir()
	depDir := filepath.Join(tmpDir, "dep-service")
	_ = os.MkdirAll(depDir, 0o755)
	_ = os.WriteFile(filepath.Join(depDir, "main.go"), []byte("package main\n\nfunc Run() { NewServer() }\n"), 0o644)

	res, err := srv.handleCrossRepoImpact(ctx, map[string]any{
		"target_symbol": "NewServer",
		"limit":         10,
	})
	if err != nil {
		t.Fatalf("handleCrossRepoImpact failed: %v", err)
	}

	if !strings.Contains(res, "NewServer") {
		t.Errorf("expected NewServer in report, got: %s", res)
	}
}
