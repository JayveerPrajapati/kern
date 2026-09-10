package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestDiskIndexViewReportsPersistedIndex pins the kern health disk view: it
// summarizes the persisted index.json (not the empty session view of a fresh
// process), and reports "rebuild required" instead of silently zeroes when
// the on-disk schema is older than this binary's.
func TestDiskIndexViewReportsPersistedIndex(t *testing.T) {
	root := t.TempDir()
	if got := diskIndexView(root); got != nil {
		t.Fatalf("diskIndexView(empty dir) = %v, want nil", got)
	}

	ix := index.New(root)
	wantVersion := ix.Version
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	got := diskIndexView(root)
	if got == nil || got["version"] != wantVersion {
		t.Fatalf("diskIndexView = %v, want version %d", got, wantVersion)
	}
	if got["files"] != 0 {
		t.Fatalf("diskIndexView files = %v, want 0 (empty index)", got["files"])
	}
}

// TestDiskIndexViewReportsRebuildRequired pins the schema-mismatch path: an
// older on-disk index must surface as "rebuild required", never as silent
// zeroes.
func TestDiskIndexViewReportsRebuildRequired(t *testing.T) {
	root := t.TempDir()
	p := filepath.Join(root, ".kern")
	if err := os.MkdirAll(p, 0o755); err != nil {
		t.Fatal(err)
	}
	// A version-0 index.json: Load rejects it with "rebuild required".
	if err := os.WriteFile(filepath.Join(p, "index.json"), []byte(`{"root":"x","version":0}`), 0o644); err != nil {
		t.Fatal(err)
	}
	got := diskIndexView(root)
	if got == nil {
		t.Fatal("diskIndexView = nil, want rebuild_required report")
	}
	if got["rebuild_required"] == nil || got["version"] != 0 {
		t.Fatalf("diskIndexView = %v, want version 0 + rebuild_required", got)
	}
}

// TestRunMCPToolSuccess pins the success path: runMCPTool prints the tool's
// output to stdout. kern_schema_validate is deterministic and local (no
// network, no index build), so it is a stable, cheap probe.
func TestRunMCPToolSuccess(t *testing.T) {
	out := captureStdout(t, func() {
		runMCPTool("kern_schema_validate", map[string]any{
			"data":   `{"name":"x"}`,
			"schema": `{"type":"object","required":["name"],"properties":{"name":{"type":"string"}}}`,
		})
	})
	if !strings.Contains(out, "schema OK") {
		t.Fatalf("runMCPTool output = %q, want schema OK message", out)
	}
}

// TestRunMCPToolUnknownToolPanics pins the error path: an unknown tool must
// panic with the exitError{code:1} sentinel so main() converts it into a
// process exit.
func TestRunMCPToolUnknownToolPanics(t *testing.T) {
	defer func() {
		r := recover()
		if r == nil {
			t.Fatal("runMCPTool(unknown tool) did not panic")
		}
		ee, ok := r.(exitError)
		if !ok {
			t.Fatalf("panic value = %T(%v), want exitError", r, r)
		}
		if ee.code != 1 {
			t.Fatalf("exitError.code = %d, want 1", ee.code)
		}
	}()
	runMCPTool("definitely_not_a_tool", nil)
}
