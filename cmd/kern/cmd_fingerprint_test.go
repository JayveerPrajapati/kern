package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fingerprintFixture returns a dir with: one Go file with functions, one
// Go file without any top-level functions, and one non-Go file.
func fingerprintFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(dir, rel), []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", rel, err)
		}
	}
	write("main.go", "package main\n\nfunc hello() string { return \"h\" }\n\nfunc main() { _ = hello() }\n")
	write("blank.go", "package main\n\nimport _ \"fmt\"\n")
	write("script.py", "print('hi')\n")
	return dir
}

// TestFingerprintEmptyIsLoud pins the e2e round-2 fix: empty fingerprint
// results must explain themselves on stderr instead of silently printing
// nothing (or JSON null).
func TestFingerprintEmptyIsLoud(t *testing.T) {
	dir := fingerprintFixture(t)

	// Walk mode over a dir with no Go files at all.
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "x.py"), []byte("print('x')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	errOut := captureStderr(t, func() {
		runFingerprint([]string{dir2})
	})
	if !strings.Contains(errOut, "no Go source files found") {
		t.Fatalf("expected loud no-Go-files message on stderr, got: %q", errOut)
	}

	// Explicit --file naming a non-Go file: warned and skipped.
	errOut = captureStderr(t, func() {
		runFingerprint([]string{"--file", filepath.Join(dir, "script.py")})
	})
	if !strings.Contains(errOut, "not a Go source file") {
		t.Fatalf("expected non-Go skip warning on stderr, got: %q", errOut)
	}

	// A Go file without top-level functions: counted and explained.
	errOut = captureStderr(t, func() {
		runFingerprint([]string{"--file", filepath.Join(dir, "blank.go")})
	})
	if !strings.Contains(errOut, "no fingerprints emitted") || !strings.Contains(errOut, "1 without top-level functions") {
		t.Fatalf("expected no-fingerprints summary on stderr, got: %q", errOut)
	}
}

// TestFingerprintJSONEmptyArrayNotNull pins the JSON contract: an empty
// result set marshals as [] (not null), and real Go files still produce
// records.
func TestFingerprintJSONEmptyArrayNotNull(t *testing.T) {
	dir := fingerprintFixture(t)
	dir2 := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir2, "x.py"), []byte("print('x')\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	jsonOut := ""
	captureStderr(t, func() {
		jsonOut = captureStdout(t, func() {
			runFingerprint([]string{dir2, "--json"})
		})
	})
	if strings.Contains(jsonOut, "null") {
		t.Fatalf("expected JSON [] not null, got: %s", jsonOut)
	}
	if !strings.Contains(jsonOut, `"fingerprints": []`) {
		t.Fatalf("expected empty fingerprints array, got: %s", jsonOut)
	}

	jsonOut = captureStdout(t, func() {
		runFingerprint([]string{"--file", filepath.Join(dir, "main.go"), "--json"})
	})
	if !strings.Contains(jsonOut, `"name": "hello"`) {
		t.Fatalf("expected hello fingerprint record, got: %s", jsonOut)
	}
}
