package sandbox

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// hubProject writes a Go project whose Hub function has 11 callers (HIGH
// pre-edit verdict) and returns the root.
func hubProject(t *testing.T) string {
	t.Helper()
	root := t.TempDir()
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(root, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module demo\n\ngo 1.22\n")
	write("hub.go", "package p\n\nfunc Hub() int { return 0 }\n")
	for i := 0; i < 11; i++ {
		write(fmt.Sprintf("c%d.go", i), fmt.Sprintf("package p\n\nfunc C%d() int { return Hub() }\n", i))
	}
	return root
}

// TestRunGuardedRestoresHighRiskKeep pins the P2 sandbox gate: a successful
// command that rewrites a HIGH-risk file is rolled back unless forced.
func TestRunGuardedRestoresHighRiskKeep(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	root := hubProject(t)
	hubPath := filepath.Join(root, "hub.go")

	res := RunGuarded(context.Background(), root, "sh", []string{"-c", "echo '// gate' >> hub.go"}, 30*time.Second, false)
	if res.OK {
		t.Fatalf("expected gate refusal, got success: %+v", res)
	}
	if !res.Restored {
		t.Fatalf("expected restore on HIGH keep without force: %+v", res)
	}
	if res.Err == nil || !strings.Contains(res.Err.Error(), "--force") {
		t.Fatalf("refusal must name --force, got %+v", res.Err)
	}
	if body, _ := os.ReadFile(hubPath); strings.Contains(string(body), "gate") {
		t.Fatalf("hub.go must be rolled back, got:\n%s", body)
	}
}

// TestRunGuardedForceKeepsHighRiskChange pins the override: force=true keeps
// a HIGH-risk keep.
func TestRunGuardedForceKeepsHighRiskChange(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	root := hubProject(t)

	res := RunGuarded(context.Background(), root, "sh", []string{"-c", "echo '// gate' >> hub.go"}, 30*time.Second, true)
	if !res.OK {
		t.Fatalf("expected success with force, got %+v", res.Err)
	}
	if res.Restored {
		t.Fatal("forced keep must not restore")
	}
	body, _ := os.ReadFile(filepath.Join(root, "hub.go"))
	if !strings.Contains(string(body), "gate") {
		t.Fatalf("forced change must be kept, got:\n%s", body)
	}
}

// TestRunGuardedLowRiskKeepUnaffected pins gate transparency: touching a
// low-risk file keeps working without force.
func TestRunGuardedLowRiskKeepUnaffected(t *testing.T) {
	t.Setenv("KERN_ALLOW_UNISOLATED", "1")
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644)
	_ = os.WriteFile(filepath.Join(root, "solo.go"), []byte("package p\n\nfunc Solo() {}\n"), 0o644)

	res := RunGuarded(context.Background(), root, "sh", []string{"-c", "echo '// gate' >> solo.go"}, 30*time.Second, false)
	if !res.OK {
		t.Fatalf("low-risk keep must succeed without force, got %+v", res.Err)
	}
	if res.Restored {
		t.Fatal("low-risk keep must not restore")
	}
}
