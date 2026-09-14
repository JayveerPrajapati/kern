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
