package crossrepo

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestImpact(t *testing.T) {
	tmpDir := t.TempDir()
	depDir := filepath.Join(tmpDir, "dep-service")
	_ = os.MkdirAll(depDir, 0o755)
	_ = os.WriteFile(filepath.Join(depDir, "main.go"), []byte("package main\n\nfunc Run() { NewServer() }\n"), 0o644)

	// Text
	res, err := Impact("NewServer", 10, "")
	if err != nil {
		t.Fatalf("Impact failed: %v", err)
	}
	if !strings.Contains(res, "NewServer") {
		t.Errorf("expected NewServer in report, got: %s", res)
	}

	// JSON
	resJSON, err := Impact("NewServer", 10, "json")
	if err != nil {
		t.Fatalf("Impact JSON failed: %v", err)
	}
	if !strings.Contains(resJSON, `"subject": "NewServer"`) {
		t.Errorf("expected json subject field, got: %s", resJSON)
	}

	if _, err := Impact("", 10, ""); err == nil {
		t.Error("expected error on empty subject")
	}
}
