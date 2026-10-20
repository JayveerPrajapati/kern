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

// TestExecuteTransactionScrubsOperatorEnv pins the env allowlist at the
// engine boundary: a sentinel operator var must never reach the compile
// child, while PATH (the allowlist's core) still does. The compile command
// `env` exits 0, so a leaked sentinel would appear in CompilerOutput.
func TestExecuteTransactionScrubsOperatorEnv(t *testing.T) {
	const sentinel = "KERN_TEST_SECRET_1a2b3c4d_value"
	t.Setenv("KERN_TEST_SECRET", sentinel)

	dir := t.TempDir()
	_ = os.WriteFile(filepath.Join(dir, "a.go"), []byte("package main\n\nfunc A() int { return 1 }\n"), 0o644)

	res, err := ExecuteTransaction(context.Background(), TransactionRequest{
		Root:           dir,
		Edits:          []FileEdit{{Path: "a.go", Content: "package main\n\nfunc A() int { return 2 }\n"}},
		CompileCommand: "env",
	})
	if err != nil {
		t.Fatalf("ExecuteTransaction error: %v", err)
	}
	if !res.Success {
		t.Fatalf("expected success (env exits 0), got success=%v rolled_back=%v err=%s", res.Success, res.RolledBack, res.Error)
	}
	if !strings.Contains(res.CompilerOutput, "PATH=") {
		t.Errorf("expected the allowlisted PATH in compiler output, got: %.400s", res.CompilerOutput)
	}
	if strings.Contains(res.CompilerOutput, sentinel) {
		t.Errorf("compile child received the operator env: KERN_TEST_SECRET leaked into %q", res.CompilerOutput)
	}
}

// TestTransactionalRefactorRejectsSymlinkEscape pins the symlink-containment
// fix: an edit whose path is a symlink pointing OUTSIDE the project root must
// be rejected (fail closed), and the symlink's target must never be written.
func TestTransactionalRefactorRejectsSymlinkEscape(t *testing.T) {
	root := t.TempDir()
	outside := filepath.Join(t.TempDir(), "outside.txt")
	if err := os.WriteFile(outside, []byte("secret\n"), 0o644); err != nil {
		t.Fatalf("write outside file: %v", err)
	}
	link := filepath.Join(root, "link.txt")
	if err := os.Symlink(outside, link); err != nil {
		t.Skipf("symlinks unsupported: %v", err)
	}

	_, err := ExecuteTransaction(context.Background(), TransactionRequest{
		Root:  root,
		Edits: []FileEdit{{Path: "link.txt", Content: "pwned\n"}},
		Apply: true,
	})
	if err == nil {
		t.Fatal("expected symlink-escape edit to be rejected, got nil")
	}
	if !strings.Contains(err.Error(), "escapes workspace root") {
		t.Errorf("error = %q, want it to name the symlink containment escape", err)
	}
	outsideContent, _ := os.ReadFile(outside)
	if strings.Contains(string(outsideContent), "pwned") {
		t.Error("symlink-escape edit wrote through the symlink to the outside file")
	}
}

// TestTransactionalRefactorAbortsOnConcurrentEdit pins the TOCTOU preflight:
// a compile command that mutates the live tree between snapshot and apply must
// abort the apply before any write lands, so the concurrent edit is preserved.
func TestTransactionalRefactorAbortsOnConcurrentEdit(t *testing.T) {
	root := t.TempDir()
	if strings.ContainsAny(root, " \t") {
		t.Skipf("temp dir contains whitespace, compile command would split: %q", root)
	}
	mainPath := filepath.Join(root, "main.go")
	if err := os.WriteFile(mainPath, []byte("package main\n"), 0o644); err != nil {
		t.Fatalf("write main.go: %v", err)
	}
	// Mutator script appends to the ABSOLUTE live main.go path; it runs as the
	// compile command (cmd.Dir is the sandbox, so only the absolute path can
	// reach the live tree).
	mutator := filepath.Join(root, "mutate.sh")
	script := "#!/bin/sh\nprintf 'hacked\\n' >> " + mainPath + "\n"
	if err := os.WriteFile(mutator, []byte(script), 0o755); err != nil {
		t.Fatalf("write mutator: %v", err)
	}

	_, err := ExecuteTransaction(context.Background(), TransactionRequest{
		Root:           root,
		Edits:          []FileEdit{{Path: "main.go", Content: "package main // edited\n"}},
		CompileCommand: "sh " + mutator,
		Apply:          true,
	})
	if err == nil {
		t.Fatal("expected concurrent-edit abort, got nil")
	}
	if !strings.Contains(err.Error(), "changed on disk") {
		t.Errorf("error = %q, want it to report the file changed on disk", err)
	}
	mainContent, _ := os.ReadFile(mainPath)
	if strings.Contains(string(mainContent), "// edited") {
		t.Error("apply clobbered the concurrently-edited main.go")
	}
}
