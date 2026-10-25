package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunApplyEndToEnd runs `kern apply` live against a temp Go module:
// a passing edit applies and exits 0; a verify-breaking edit rolls the live
// tree back and exits 1. Pinned exit contract: 0 applied+verified, 1
// apply/verify failed.
func TestRunApplyEndToEnd(t *testing.T) {
	dir := t.TempDir()
	write := func(rel, content string) {
		t.Helper()
		full := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("go.mod", "module example.com/m\n\ngo 1.27\n")
	write("lib.go", "package m\n\nfunc Lib() int { return 1 }\n")
	write("lib_test.go", "package m\n\nimport \"testing\"\n\nfunc TestLib(t *testing.T) {\n\tif Lib() != 1 {\n\t\tt.Fatal(\"Lib changed\")\n\t}\n}\n")
	edits := func(json string) string {
		t.Helper()
		p := filepath.Join(dir, "edits.json")
		if err := os.WriteFile(p, []byte(json), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	// 1. Passing edit: applied, verified, exit 0.
	ok := edits(`[{"path": "lib.go", "content": "package m\n\nfunc Lib() int { return 1 }\nfunc Extra() int { return 42 }\n"}]`)
	out := captureStdout(t, func() {
		if code := runApply([]string{ok, "--root", dir, "--test"}); code != 0 {
			t.Errorf("passing edit: exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "APPLIED") || !strings.Contains(out, "receipt:") {
		t.Errorf("output missing APPLIED/receipt:\n%s", out)
	}
	if _, err := os.Stat(filepath.Join(dir, "lib.go")); err != nil {
		t.Fatalf("applied file missing: %v", err)
	}

	// 2. Verify-breaking edit: rolled back, exit 1.
	breaking := edits(`[{"path": "lib.go", "content": "package m\n\nfunc Lib() int { return 2 }\n"}]`)
	out = captureStdout(t, func() {
		if code := runApply([]string{breaking, "--root", dir, "--test"}); code != 1 {
			t.Errorf("breaking edit: exit = %d, want 1", code)
		}
	})
	if !strings.Contains(out, "ROLLED BACK") {
		t.Errorf("output missing ROLLED BACK:\n%s", out)
	}
	after, err := os.ReadFile(filepath.Join(dir, "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(after), "Extra()") {
		t.Fatal("live tree not restored to pre-breaking content")
	}

	// 3. Dry-run: preview only, tree untouched, exit 0.
	dry := edits(`[{"path": "lib.go", "content": "package m\n\nfunc Lib() int { return 1 }\nfunc Extra() int { return 42 }\nfunc Never() int { return 7 }\n"}]`)
	out = captureStdout(t, func() {
		if code := runApply([]string{dry, "--root", dir, "--test", "--dry-run"}); code != 0 {
			t.Errorf("dry-run: exit = %d, want 0", code)
		}
	})
	if !strings.Contains(out, "PREVIEWED") {
		t.Errorf("output missing PREVIEWED:\n%s", out)
	}
	after, err = os.ReadFile(filepath.Join(dir, "lib.go"))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(after), "Never()") {
		t.Fatal("dry-run must not touch the live tree")
	}
}

// TestRunApplyUsage pins the usage-error exit (2) and the missing-file
// runtime error (1).
func TestRunApplyUsage(t *testing.T) {
	captureStdout(t, func() {
		defer func() {
			r := recover()
			if r == nil {
				t.Error("missing argument must fatalUsage (exit 2)")
			}
		}()
		runApply([]string{})
	})
	captureStdout(t, func() {
		defer func() {
			if r := recover(); r == nil {
				t.Error("missing edits file must fatal (exit 1)")
			}
		}()
		runApply([]string{"/nonexistent/edits.json"})
	})
}
