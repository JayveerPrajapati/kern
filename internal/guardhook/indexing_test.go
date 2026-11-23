package guardhook

import (
	"bytes"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestIndexingMarkerFreshAcceptsFileAndDirectory(t *testing.T) {
	root := t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, ".kern"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".kern", "config.json"), []byte(`{"schema_version":1,"managed":true,"guard":{"enabled":true}}`), 0o600); err != nil {
		t.Fatal(err)
	}

	source := filepath.Join(root, "main.go")
	if err := os.WriteFile(source, []byte("package main\n"+strings.Repeat("// fixture line\n", 160)), 0o644); err != nil {
		t.Fatal(err)
	}
	payload := fmt.Sprintf(`{"tool_name":"Read","tool_input":{"file_path":%q}}`, source)

	run := func() int {
		t.Helper()
		var stdout, stderr bytes.Buffer
		return Run(strings.NewReader(payload), &stdout, &stderr)
	}

	t.Run("no marker is governed", func(t *testing.T) {
		t.Setenv("CLAUDE_PROJECT_DIR", root)
		t.Setenv("KERN_GUARD_ASSUME_KERN", "0")
		if code := run(); code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
	})

	marker := filepath.Join(root, ".kern", "indexing")
	if err := os.WriteFile(marker, []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("fresh file marker opens the guard", func(t *testing.T) {
		t.Setenv("CLAUDE_PROJECT_DIR", root)
		t.Setenv("KERN_GUARD_ASSUME_KERN", "0")
		if code := run(); code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
	})

	t.Run("stale file marker stays governed", func(t *testing.T) {
		old := time.Now().Add(-30 * time.Minute)
		if err := os.Chtimes(marker, old, old); err != nil {
			t.Fatal(err)
		}
		t.Setenv("CLAUDE_PROJECT_DIR", root)
		t.Setenv("KERN_GUARD_ASSUME_KERN", "0")
		if code := run(); code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
	})

	if err := os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(marker, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(marker, "current"), []byte("1\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Run("fresh directory marker opens the guard", func(t *testing.T) {
		t.Setenv("CLAUDE_PROJECT_DIR", root)
		t.Setenv("KERN_GUARD_ASSUME_KERN", "0")
		if code := run(); code != 0 {
			t.Fatalf("exit = %d, want 0", code)
		}
	})

	entry := filepath.Join(marker, "current")
	old := time.Now().Add(-30 * time.Minute)
	if err := os.Chtimes(entry, old, old); err != nil {
		t.Fatal(err)
	}

	t.Run("stale directory marker stays governed", func(t *testing.T) {
		t.Setenv("CLAUDE_PROJECT_DIR", root)
		t.Setenv("KERN_GUARD_ASSUME_KERN", "0")
		if code := run(); code != 2 {
			t.Fatalf("exit = %d, want 2", code)
		}
	})
}
