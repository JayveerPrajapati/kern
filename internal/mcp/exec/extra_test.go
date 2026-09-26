package exec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDiffFilesIdentical(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a.txt")
	if err := os.WriteFile(a, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	b := filepath.Join(dir, "b.txt")
	if err := os.WriteFile(b, []byte("hello\nworld\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := DiffFiles(context.Background(), Hooks{}, map[string]any{
		"root": dir,
		"a":    "a.txt",
		"b":    "b.txt",
	})
	if err != nil {
		t.Fatalf("DiffFiles: %v", err)
	}
	if out != "files identical" {
		t.Fatalf("expected 'files identical', got %q", out)
	}
}

func TestDiffFilesRequiresAB(t *testing.T) {
	ctx := context.Background()
	if _, err := DiffFiles(ctx, Hooks{}, map[string]any{}); err == nil || !strings.Contains(err.Error(), "a and b are required") {
		t.Fatalf("expected a and b required error, got: %v", err)
	}
}

func TestDiffFilesRejectsAbsolutePathWithoutRoot(t *testing.T) {
	ctx := context.Background()
	_, err := DiffFiles(ctx, Hooks{}, map[string]any{
		"a": "/etc/passwd",
		"b": "b.txt",
	})
	if err == nil || !strings.Contains(err.Error(), "absolute path requires root argument") {
		t.Fatalf("expected absolute path rejection without root, got: %v", err)
	}
}

func TestHealFailsClosedWithoutGovernance(t *testing.T) {
	// heal drives arbitrary host commands, so without an allowlist or
	// KERN_ALLOW_EXEC the governance gate must refuse before any execution.
	t.Setenv("KERN_ALLOW_EXEC", "")
	t.Setenv("KERN_TOOLS", "")
	ctx := context.Background()
	_, err := Heal(ctx, map[string]any{
		"root": t.TempDir(),
		"task": "run arbitrary host code",
	})
	if err == nil {
		t.Fatal("expected governance denial without allowlist")
	}
	if !strings.Contains(err.Error(), "governance") && !strings.Contains(err.Error(), "command execution blocked") {
		t.Fatalf("expected a governance denial error, got: %v", err)
	}
}
