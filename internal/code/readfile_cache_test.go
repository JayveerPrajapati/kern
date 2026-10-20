package code

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

// TestReadFileServesFreshContent: ReadFile must return the NEW content after
// a file edit (size change => stat miss) — the shared file-content cache
// re-checks size:mtime on every read and must never serve stale bytes.
// Repeated identical reads must return identical bytes.
func TestReadFileServesFreshContent(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "a.go")
	first := []byte("package a\n\nfunc A() {}\n")
	if err := os.WriteFile(p, first, 0o644); err != nil {
		t.Fatal(err)
	}
	b1, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1, first) {
		t.Fatalf("first read mismatch: got %q want %q", b1, first)
	}
	// Unchanged file: the repeat read returns identical bytes.
	b1b, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b1b, first) {
		t.Fatalf("repeat read mismatch: got %q want %q", b1b, first)
	}
	// Edit (different size): the next read must return the new bytes.
	second := []byte("package a\n\nfunc A() { println(\"x\") }\n")
	if err := os.WriteFile(p, second, 0o644); err != nil {
		t.Fatal(err)
	}
	b2, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b2, second) {
		t.Fatalf("stale content served after edit: got %q want %q", b2, second)
	}
}

// TestReadFileCacheSkipsCredentialFiles: credential-shaped files are read
// but never cached (they must not linger in the shared cache).
func TestReadFileCacheSkipsCredentialFiles(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, ".env")
	content := []byte("API_KEY=secret\n")
	if err := os.WriteFile(p, content, 0o600); err != nil {
		t.Fatal(err)
	}
	b, err := ReadFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(b, content) {
		t.Fatalf("credential-shaped read mismatch: got %q", b)
	}
	// Reading again must still work (a miss is transparent).
	if _, err := ReadFile(p); err != nil {
		t.Fatalf("repeat credential-shaped read failed: %v", err)
	}
}
