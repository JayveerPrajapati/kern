package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSearchJSONHasVersion(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testsearchjson\n\ngo 1.20\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "search.go"), []byte("package main\n\nfunc SearchTarget() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	out := captureStdout(t, func() {
		runSearch([]string{"SearchTarget", "--root", dir, "--json"})
	})

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("search --json failed to parse as json: %v\nOutput:\n%s", err, out)
	}

	if _, ok := parsed["version"]; !ok {
		t.Errorf("expected 'version' key in search JSON output: %v", parsed)
	}
	if parsed["total"] != float64(1) {
		t.Errorf("expected total == 1, got %v", parsed["total"])
	}
}

func TestHealthJSONHasVersion(t *testing.T) {
	out := captureStdout(t, func() {
		runHealth([]string{"--root", "."})
	})

	var parsed map[string]any
	if err := json.Unmarshal([]byte(out), &parsed); err != nil {
		t.Fatalf("health output failed to parse as json: %v\nOutput:\n%s", err, out)
	}

	if _, ok := parsed["version"]; !ok {
		t.Errorf("expected 'version' key in health output: %v", parsed)
	}
}
