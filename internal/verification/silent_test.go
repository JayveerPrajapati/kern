package verification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// writeFixture creates a tiny Go module fixture: two files in package demo
// with a Greet function and callers.
func writeFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":    "module demo\n\ngo 1.22\n",
		"main.go":   "package demo\n\n// Greet says hello.\nfunc Greet(name string) string { return \"hi \" + name }\n\n// Run calls Greet.\nfunc Run() string { return Greet(\"x\") }\n",
		"caller.go": "package demo\n\n// Caller invokes Run.\nfunc Caller() string { return Run() }\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestVerifyFullPipeline drives the whole silent pipeline against the tiny
// fixture: envelope valid, plan produced, handles resolved, injected and
// extracted — while the REAL tree is never modified (no AGENTS.md in root).
func TestVerifyFullPipeline(t *testing.T) {
	root := writeFixture(t)
	rep := VerifyFullPipeline(root, "Greet")
	if !rep.EnvelopeValid {
		t.Error("EnvelopeValid should be true")
	}
	if !rep.PlanProduced {
		t.Error("PlanProduced should be true")
	}
	if !rep.HandlesResolved {
		t.Error("HandlesResolved should be true")
	}
	if !rep.Injected {
		t.Error("Injected should be true")
	}
	if !rep.Extracted {
		t.Error("Extracted should be true")
	}
	if len(rep.Steps) == 0 {
		t.Error("Steps should be non-empty")
	}
	// The real tree is NOT modified: no AGENTS.md created in root (injection
	// happened only in the temp copy).
	if _, err := os.Stat(filepath.Join(root, "AGENTS.md")); !os.IsNotExist(err) {
		t.Errorf("real tree must not gain AGENTS.md (stat err = %v)", err)
	}
}

// TestVerifySilentOrchestration proves the kern-invisibility check: the
// fixture renders clean (no marker leaks), and injecting a marker into the
// input change string produces violations.
func TestVerifySilentOrchestration(t *testing.T) {
	root := writeFixture(t)
	silent, violations := VerifySilentOrchestration(root, "Greet")
	if !silent || len(violations) != 0 {
		t.Errorf("clean fixture should be silent, silent=%v violations=%v", silent, violations)
	}

	_, violations = VerifySilentOrchestration(root, "Greet internal/leak")
	if len(violations) == 0 {
		t.Error("marker injected into the input change string should produce violations")
	}
}

// writeScanFixture creates a Go module fixture with a single package under a
// subdirectory, so file scans (exact match) and directory scans (prefix
// match) both exercise ScanSilent's path matching.
func writeScanFixture(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"go.mod":   "module demo\n\ngo 1.22\n",
		"pkg/a.go": "package pkg\n\n// Alpha returns one.\nfunc Alpha() int { return 1 }\n",
		"pkg/b.go": "package pkg\n\n// Beta returns two.\nfunc Beta() int { return 2 }\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return root
}

// TestScanSilentFile scans a single file: the exact-match rule picks up every
// symbol declared in it.
func TestScanSilentFile(t *testing.T) {
	root := writeScanFixture(t)
	rep, err := ScanSilent(root, "pkg/a.go", 0)
	if err != nil {
		t.Fatalf("ScanSilent: %v", err)
	}
	if rep.Path != "pkg/a.go" {
		t.Errorf("Path = %q, want %q", rep.Path, "pkg/a.go")
	}
	if rep.Symbols < 1 {
		t.Errorf("Symbols = %d, want >= 1", rep.Symbols)
	}
}

// TestScanSilentDir scans a directory: the prefix rule covers every file
// under it.
func TestScanSilentDir(t *testing.T) {
	root := writeScanFixture(t)
	rep, err := ScanSilent(root, "pkg", 0)
	if err != nil {
		t.Fatalf("ScanSilent: %v", err)
	}
	if rep.Path != "pkg" {
		t.Errorf("Path = %q, want %q", rep.Path, "pkg")
	}
	if rep.Symbols < 1 {
		t.Errorf("Symbols = %d, want >= 1 (dir scan must cover its files)", rep.Symbols)
	}
}

// TestScanSilentMissingPath scans a path that does not exist: error, not a
// zero-value report.
func TestScanSilentMissingPath(t *testing.T) {
	root := writeScanFixture(t)
	_, err := ScanSilent(root, "pkg/nope", 0)
	if err == nil {
		t.Fatal("ScanSilent should fail for a missing scan path")
	}
	if !strings.Contains(err.Error(), "not found") {
		t.Errorf("error = %v, want it to mention the missing path", err)
	}
}

// TestScanSilentLimit caps the number of scanned symbols at limit.
func TestScanSilentLimit(t *testing.T) {
	root := writeScanFixture(t)
	rep, err := ScanSilent(root, "pkg", 1)
	if err != nil {
		t.Fatalf("ScanSilent: %v", err)
	}
	if rep.Symbols != 1 {
		t.Errorf("Symbols = %d, want 1 (limit)", rep.Symbols)
	}
}

// TestScanSilentEmpty scans a directory with no indexable files: zero
// symbols, no error.
func TestScanSilentEmpty(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rep, err := ScanSilent(root, ".", 0)
	if err != nil {
		t.Fatalf("ScanSilent: %v", err)
	}
	if rep.Symbols != 0 {
		t.Errorf("Symbols = %d, want 0", rep.Symbols)
	}
}

// TestCopyTreeToTempSkipsSymlinks guards AUD-04: copyTreeToTemp must never
// follow a symlink out of root. A repo symlink (notes -> external secret
// file, or a symlinked dir) must be skipped entirely — neither the link nor
// its target's content may appear in the copied tree, while regular files
// still copy normally.
func TestCopyTreeToTempSkipsSymlinks(t *testing.T) {
	root := t.TempDir()

	// Secret target OUTSIDE root (the thing a symlink must not leak).
	secret := filepath.Join(t.TempDir(), "id_rsa")
	if err := os.WriteFile(secret, []byte("SUPER-SECRET-KEY-MATERIAL"), 0o600); err != nil {
		t.Fatal(err)
	}
	secretDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(secretDir, "data.txt"), []byte("TOP-SECRET-DIR-CONTENT"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Regular file in the repo.
	if err := os.WriteFile(filepath.Join(root, "main.go"), []byte("package demo\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Symlink pointing outside root.
	if err := os.Symlink(secret, filepath.Join(root, "notes")); err != nil {
		t.Skipf("symlink unsupported on this platform: %v", err)
	}
	// Symlinked dir pointing outside root.
	if err := os.Symlink(secretDir, filepath.Join(root, "secretdir")); err != nil {
		t.Fatal(err)
	}

	dst, err := copyTreeToTemp(root)
	if err != nil {
		t.Fatalf("copyTreeToTemp: %v", err)
	}
	defer os.RemoveAll(dst)

	// The symlink entries themselves must not exist in the copy.
	for _, rel := range []string{"notes", "secretdir"} {
		if _, err := os.Lstat(filepath.Join(dst, rel)); !os.IsNotExist(err) {
			t.Errorf("symlink %q must not be copied (Lstat err = %v)", rel, err)
		}
	}
	// The symlink targets' content must NOT be present in the copied tree.
	for _, rel := range []string{"notes", "secretdir/data.txt"} {
		data, err := os.ReadFile(filepath.Join(dst, rel))
		if err == nil {
			t.Errorf("symlink target content leaked into copied tree at %q: %q", rel, string(data))
		} else if !os.IsNotExist(err) {
			t.Errorf("unexpected error reading %q: %v", rel, err)
		}
	}
	// The regular file still copies normally.
	if data, err := os.ReadFile(filepath.Join(dst, "main.go")); err != nil || string(data) != "package demo\n" {
		t.Errorf("regular file must still be copied, data=%q err=%v", data, err)
	}
}

// TestVerifyTokenReduction proves token reduction without critical-evidence
// loss via the eval harness: the symbol name and its file path survive the
// 50% budget fit.
func TestVerifyTokenReduction(t *testing.T) {
	root := writeFixture(t)
	res, err := VerifyTokenReduction(root, "Greet")
	if err != nil {
		t.Fatalf("VerifyTokenReduction: %v", err)
	}
	if res.TokenReduction <= 0 {
		t.Errorf("TokenReduction = %v, want > 0", res.TokenReduction)
	}
	if res.EvidenceRetention != 1.0 {
		t.Errorf("EvidenceRetention = %v, want 1.0", res.EvidenceRetention)
	}
}
