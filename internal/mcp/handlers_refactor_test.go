package mcp

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRefactorTransactionViaMCP(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module txmcp\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "foo.go"), []byte("package main\n\nfunc Foo() string { return \"old\" }\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "main.go"), []byte("package main\n\nfunc main() { println(Foo()) }\n"), 0o644)

	s := NewServer(strings.NewReader(""), nil)
	defer s.Close()

	// 1. Dry run preview transaction
	res, err := s.handleRefactorTransaction(context.Background(), map[string]any{
		"root": root,
		"edits": []any{
			map[string]any{"path": "foo.go", "content": "package main\n\nfunc Foo() string { return \"new\" }\n"},
		},
		"apply": "false",
	})
	if err != nil {
		t.Fatalf("handleRefactorTransaction dry-run failed: %v", err)
	}
	if !strings.Contains(res, "SUCCESSFUL") || !strings.Contains(res, "Previewed") {
		t.Errorf("expected SUCCESSFUL and Previewed in dry-run result, got:\n%s", res)
	}

	// 2. Commit transaction
	res, err = s.handleRefactorTransaction(context.Background(), map[string]any{
		"root": root,
		"edits": []any{
			map[string]any{"path": "foo.go", "content": "package main\n\nfunc Foo() string { return \"committed\" }\n"},
		},
		"apply": "true",
	})
	if err != nil {
		t.Fatalf("handleRefactorTransaction apply failed: %v", err)
	}
	if !strings.Contains(res, "SUCCESSFUL") || !strings.Contains(res, "Committed") {
		t.Errorf("expected SUCCESSFUL and Committed in result, got:\n%s", res)
	}

	// Verify disk file
	fooData, _ := os.ReadFile(filepath.Join(root, "foo.go"))
	if !strings.Contains(string(fooData), "committed") {
		t.Errorf("expected foo.go updated on disk, got: %s", string(fooData))
	}
}
