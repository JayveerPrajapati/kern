package refactor

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestTransactionalRefactorSuccess(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testtx\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc A() int { return 1 }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println(A()) }\n"), 0o644)

	// Valid multi-file edit: change A() to return string, update main() to print string
	edits := []FileEdit{
		{Path: "a.go", Content: "package main\n\nfunc A() string { return \"hello\" }\n"},
		{Path: "main.go", Content: "package main\n\nfunc main() { println(A() + \" world\") }\n"},
	}

	res, err := ExecuteTransaction(context.Background(), TransactionRequest{
		Root:  dir,
		Edits: edits,
		Apply: true,
	})
	if err != nil {
		t.Fatalf("ExecuteTransaction error: %v", err)
	}

	if !res.Success || res.RolledBack {
		t.Errorf("expected success=true, rolled_back=false, got success=%v, rolled_back=%v, err=%s", res.Success, res.RolledBack, res.Error)
	}

	// Verify live files updated
	aContent, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if !strings.Contains(string(aContent), "string") {
		t.Errorf("expected a.go updated on disk, got: %s", string(aContent))
	}
}

func TestTransactionalRefactorRejectsEmptyPath(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testtx\n\ngo 1.22\n"), 0o644)

	edits := []FileEdit{
		{Path: "", Content: "package main\n"},
	}

	_, err := ExecuteTransaction(context.Background(), TransactionRequest{
		Root:  dir,
		Edits: edits,
		Apply: true,
	})
	if err == nil {
		t.Fatal("expected error for empty edits[].path, got nil")
	}
	// The error must name the {path, content} shape, never surface as the
	// confusing "write .: is a directory".
	if !strings.Contains(err.Error(), "path is required") || !strings.Contains(err.Error(), "{path, content}") {
		t.Errorf("error = %q, want it to name the {path, content} shape", err)
	}
}

func TestTransactionalRefactorRejectsEscapePath(t *testing.T) {
	dir := t.TempDir()
	escaped := filepath.Join(filepath.Dir(dir), "escaped.txt")

	edits := []FileEdit{
		{Path: "../escaped.txt", Content: "should never be written\n"},
	}

	_, err := ExecuteTransaction(context.Background(), TransactionRequest{
		Root:  dir,
		Edits: edits,
		Apply: true,
	})
	if err == nil {
		t.Fatal("expected error for ../ escape path, got nil")
	}
	if !strings.Contains(err.Error(), "escapes the project root") {
		t.Errorf("error = %q, want it to report the root escape", err)
	}
	if _, statErr := os.Stat(escaped); statErr == nil {
		t.Error("escape edit wrote a file outside the project root")
	}
}

func TestTransactionalRefactorRejectsAbsoluteOutsideRoot(t *testing.T) {
	dir := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")

	edits := []FileEdit{
		{Path: outside, Content: "should never be written\n"},
	}

	_, err := ExecuteTransaction(context.Background(), TransactionRequest{
		Root:  dir,
		Edits: edits,
		Apply: true,
	})
	if err == nil {
		t.Fatal("expected error for absolute path outside root, got nil")
	}
	if !strings.Contains(err.Error(), "outside the project root") {
		t.Errorf("error = %q, want it to report the path outside the project root", err)
	}
	if _, statErr := os.Stat(outside); statErr == nil {
		t.Error("absolute-outside-root edit wrote a file outside the project root")
	}
}

func TestTransactionalRefactorNormalizesAbsoluteInsideRoot(t *testing.T) {
	dir := t.TempDir()
	// No go.mod: the sandbox skips compilation (verificationSkipped), so the
	// test asserts only the path confinement + apply behavior.
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc A() int { return 1 }\n"), 0o644)

	// An absolute path INSIDE the root is confined and normalized to
	// repo-relative: the edit must apply to the live tree at the target file.
	edits := []FileEdit{
		{Path: filepath.Join(dir, "a.go"), Content: "package main\n\nfunc A() int { return 2 }\n"},
	}

	res, err := ExecuteTransaction(context.Background(), TransactionRequest{
		Root:  dir,
		Edits: edits,
		Apply: true,
	})
	if err != nil {
		t.Fatalf("ExecuteTransaction error for absolute-inside-root path: %v", err)
	}
	if !res.Success {
		t.Errorf("expected success, got success=%v err=%s", res.Success, res.Error)
	}
	aContent, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if !strings.Contains(string(aContent), "return 2") {
		t.Errorf("expected a.go updated on disk via normalized absolute path, got: %s", string(aContent))
	}
}

func TestTransactionalRefactorRollbackOnCompileError(t *testing.T) {
	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "go.mod"), []byte("module testtx\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc A() int { return 1 }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\n\nfunc main() { println(A()) }\n"), 0o644)

	// Broken edit: syntax error in a.go
	edits := []FileEdit{
		{Path: "a.go", Content: "package main\n\nfunc A() { invalid syntax here !@# }\n"},
	}

	res, err := ExecuteTransaction(context.Background(), TransactionRequest{
		Root:  dir,
		Edits: edits,
		Apply: true,
	})
	if err != nil {
		t.Fatalf("ExecuteTransaction error: %v", err)
	}

	if res.Success || !res.RolledBack {
		t.Errorf("expected success=false, rolled_back=true, got success=%v, rolled_back=%v", res.Success, res.RolledBack)
	}

	// Verify live file untouched (atomic rollback)
	aContent, _ := os.ReadFile(filepath.Join(dir, "a.go"))
	if !strings.Contains(string(aContent), "func A() int { return 1 }") {
		t.Errorf("expected live a.go preserved, got: %s", string(aContent))
	}
}
