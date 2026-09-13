package docbudget

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func writeManifest(t *testing.T, root string, m Manifest) {
	t.Helper()
	data, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "docs")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "doc-budgets.json"), data, 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeDoc(t *testing.T, root, rel, content string) {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(rel))
	if err := os.MkdirAll(filepath.Dir(abs), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(abs, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestCountWords(t *testing.T) {
	root := t.TempDir()
	writeDoc(t, root, "docs/a.md", "one two three\n```go\nx y z\n```\nfour five")
	count, err := CountWords(filepath.Join(root, "docs/a.md"))
	if err != nil {
		t.Fatal(err)
	}
	if count != 5 {
		t.Errorf("CountWords = %d, want 5 (fenced code excluded)", count)
	}
}

func TestValidatePass(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, Manifest{Rules: []Rule{
		{Path: "AGENTS.md", MaxWords: 100},
		{Path: "docs/guide.md", MaxWords: 50},
	}})
	writeDoc(t, root, "AGENTS.md", "a b c")
	writeDoc(t, root, "docs/guide.md", "x y")
	v, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 0 {
		t.Fatalf("expected no violations, got %v", v)
	}
}

func TestValidateOverLimit(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, Manifest{Rules: []Rule{{Path: "AGENTS.md", MaxWords: 3}}})
	writeDoc(t, root, "AGENTS.md", "a b c d e")
	v, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 || v[0].Path != "AGENTS.md" {
		t.Fatalf("expected one over-limit violation, got %v", v)
	}
	if v[0].Current != 5 || v[0].Max != 3 {
		t.Errorf("violation should carry current/max: %+v", v[0])
	}
}

func TestValidateMissingDoc(t *testing.T) {
	root := t.TempDir()
	writeManifest(t, root, Manifest{Rules: []Rule{{Path: "docs/nope.md", MaxWords: 10}}})
	v, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 1 || v[0].Path != "docs/nope.md" {
		t.Fatalf("expected missing-doc violation, got %v", v)
	}
}

func TestValidateNoManifest(t *testing.T) {
	root := t.TempDir()
	v, err := Validate(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(v) != 0 {
		t.Fatalf("no manifest should mean no violations, got %v", v)
	}
}
