package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// evidenceFixture creates a temp repo with Go sources and a built index,
// so `evidence export` can run against a real index.
func evidenceFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	writeFixtureFile(t, dir, "public/a.go", `package public

func PublicA() int { return PublicB() + 1 }
`)
	writeFixtureFile(t, dir, "public/b.go", `package public

func PublicB() int { return 2 }
`)
	if _, err := index.Build(dir); err != nil {
		t.Fatalf("build index: %v", err)
	}
	return dir
}

func writeFixtureFile(t *testing.T, dir, rel, content string) {
	t.Helper()
	p := filepath.Join(dir, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(p), err)
	}
	if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", rel, err)
	}
}

// TestEvidenceExport_Stdout: `evidence export --root <dir>` emits valid
// bundle JSON on stdout and exits 0.
func TestEvidenceExport_Stdout(t *testing.T) {
	dir := evidenceFixture(t)

	var out string
	code := -1
	out = captureStdout(t, func() {
		code = runEvidence([]string{"export", "--root", dir, "--agent-id", "default", "--task", "T-1"})
	})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	var b map[string]any
	if err := json.Unmarshal([]byte(out), &b); err != nil {
		t.Fatalf("stdout is not valid JSON: %v\n%s", err, out)
	}
	if b["schema_version"] != float64(1) {
		t.Errorf("schema_version = %v, want 1", b["schema_version"])
	}
	if b["bundle_id"] == "" || b["bundle_hash"] == "" {
		t.Errorf("bundle missing id/hash: %v", b)
	}
	if b["authorization"] == nil || b["freshness"] == nil || b["lineage"] == nil {
		t.Errorf("bundle missing one of the three pillars: %v", b)
	}
}

// TestEvidenceExport_File: `--out <file>` writes the bundle to disk and
// exits 0.
func TestEvidenceExport_File(t *testing.T) {
	dir := evidenceFixture(t)
	outPath := filepath.Join(t.TempDir(), "evidence.json")

	code := runEvidence([]string{"export", "--root", dir, "--agent-id", "default", "--task", "T-1", "--out", outPath})
	if code != 0 {
		t.Fatalf("exit code = %d, want 0", code)
	}
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	var b map[string]any
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("file is not valid JSON: %v", err)
	}
	if b["bundle_hash"] == "" {
		t.Error("file bundle has no bundle_hash")
	}
}

// TestEvidenceVerify_Valid: export to a file, then verify it — VALID, exit 0.
func TestEvidenceVerify_Valid(t *testing.T) {
	dir := evidenceFixture(t)
	outPath := filepath.Join(t.TempDir(), "evidence.json")
	if code := runEvidence([]string{"export", "--root", dir, "--agent-id", "default", "--task", "T-1", "--out", outPath}); code != 0 {
		t.Fatalf("export exit code = %d, want 0", code)
	}

	var out string
	code := -1
	out = captureStdout(t, func() {
		code = runEvidence([]string{"verify", "--file", outPath})
	})
	if code != 0 {
		t.Fatalf("verify exit code = %d, want 0 (stderr above)", code)
	}
	if !strings.Contains(out, "VALID") {
		t.Errorf("verify output does not say VALID: %s", out)
	}
	if !strings.Contains(out, "Schema v1") {
		t.Errorf("verify output does not mention Schema v1: %s", out)
	}
}

// TestEvidenceVerify_Tampered: after modifying the bundle file, verify must
// exit 2 (tampered/broken).
func TestEvidenceVerify_Tampered(t *testing.T) {
	dir := evidenceFixture(t)
	outPath := filepath.Join(t.TempDir(), "evidence.json")
	if code := runEvidence([]string{"export", "--root", dir, "--agent-id", "default", "--task", "T-1", "--out", outPath}); code != 0 {
		t.Fatalf("export exit code = %d, want 0", code)
	}

	// Tamper with the bundle file.
	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read %s: %v", outPath, err)
	}
	var b map[string]any
	if err := json.Unmarshal(data, &b); err != nil {
		t.Fatalf("unmarshal bundle: %v", err)
	}
	b["generated_at"] = "1970-01-01T00:00:00Z"
	tampered, err := json.MarshalIndent(b, "", "  ")
	if err != nil {
		t.Fatalf("marshal tampered bundle: %v", err)
	}
	if err := os.WriteFile(outPath, tampered, 0o644); err != nil {
		t.Fatalf("write tampered bundle: %v", err)
	}

	code := runEvidence([]string{"verify", "--file", outPath})
	if code != 2 {
		t.Fatalf("verify exit code = %d, want 2 for a tampered bundle", code)
	}
}

// TestEvidenceVerify_ParseError: a non-JSON file is a parse error, exit 1.
func TestEvidenceVerify_ParseError(t *testing.T) {
	bad := filepath.Join(t.TempDir(), "not-a-bundle.json")
	if err := os.WriteFile(bad, []byte("this is not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if code := runEvidence([]string{"verify", "--file", bad}); code != 1 {
		t.Fatalf("verify exit code = %d, want 1 for a parse error", code)
	}
}
