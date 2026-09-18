package main

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// captureStderrExit runs fn with os.Stderr redirected to a pipe, returning
// the captured stderr and the exit code of any exitError panic fn raises
// (0 when fn returns without panicking). Non-exitError panics re-panic.
func captureStderrExit(t *testing.T, fn func()) (string, int) {
	t.Helper()
	old := os.Stderr
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stderr = w
	code := 0
	func() {
		defer func() {
			if rec := recover(); rec != nil {
				if ee, ok := rec.(exitError); ok {
					code = ee.code
				} else {
					panic(rec)
				}
			}
		}()
		fn()
	}()
	_ = w.Close()
	os.Stderr = old
	b, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(b), code
}

// TestDiskIndexViewReportsPersistedIndex pins the kern health disk view: it
// summarizes the persisted index.json (not the empty session view of a fresh
// process), and reports "rebuild required" instead of silently zeroes when
// the on-disk schema is older than this binary's.
func TestDiskIndexViewReportsPersistedIndex(t *testing.T) {
	root := t.TempDir()
	if got := index.DiskIndexView(root); got != nil {
		t.Fatalf("index.DiskIndexView(empty dir) = %v, want nil", got)
	}

	ix := index.New(root)
	wantVersion := ix.Version
	if err := ix.Save(); err != nil {
		t.Fatal(err)
	}
	got := index.DiskIndexView(root)
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
	got := index.DiskIndexView(root)
	if got == nil {
		t.Fatal("diskIndexView = nil, want rebuild_required report")
	}
	if got["rebuild_required"] == nil || got["version"] != 0 {
		t.Fatalf("diskIndexView = %v, want version 0 + rebuild_required", got)
	}
}

func TestRunHealthIndexBlockIsDiskAuthoritative(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)
	// Build a real on-disk index first (cold).
	runIndex([]string{root, "--json"})

	out := captureStdout(t, func() { runHealth([]string{"--root", root, "--json"}) })
	m := assertValidJSON(t, out)
	idx, ok := m["index"].(map[string]any)
	if !ok {
		t.Fatalf("health output missing index block: %v", m)
	}
	if idx["built"] != true {
		t.Fatalf("index.built = %v, want true (disk index authoritative): %v", idx["built"], m)
	}
	if s, ok := idx["symbols"].(float64); !ok || s < 1 {
		t.Fatalf("index.symbols = %v, want >= 1 (disk counts, not the in-memory 0): %v", idx["symbols"], m)
	}
	if f, ok := idx["files"].(float64); !ok || f < 1 {
		t.Fatalf("index.files = %v, want >= 1 (disk counts): %v", idx["files"], m)
	}
	if idx["fresh"] != true {
		t.Fatalf("index.fresh = %v, want true right after build: %v", idx["fresh"], m)
	}
	// The in-memory view must be relabeled, with an explicit note.
	mem, ok := m["mcp_memory_index"].(map[string]any)
	if !ok {
		t.Fatalf("health output missing relabeled mcp_memory_index block: %v", m)
	}
	if note, ok := mem["note"].(string); !ok || note == "" {
		t.Fatalf("mcp_memory_index.note missing: %v", mem)
	}
}

// TestRunHealthNoIndexReportsNotBuilt pins the first-run shape: with no
// persisted index, the health "index" block says so explicitly (built=false
// with a note) instead of echoing the in-memory zero block.
func TestRunHealthNoIndexReportsNotBuilt(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()

	out := captureStdout(t, func() { runHealth([]string{"--root", root}) })
	m := assertValidJSON(t, out)
	idx, ok := m["index"].(map[string]any)
	if !ok {
		t.Fatalf("health output missing index block: %v", m)
	}
	if idx["built"] != false || idx["symbols"] != float64(0) {
		t.Fatalf("index block = %v, want built=false/symbols=0", idx)
	}
	if _, ok := idx["note"].(string); !ok {
		t.Fatalf("index block missing note: %v", idx)
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

// TestRunSynthesizeTestBadFlagExits2 pins the uniform flag-error handling:
// a bad flag must exit 2 with a single "kern: flags: ..." line, never the
// stdlib flag package's raw "flag provided but not defined" + "Usage of"
// dump.
func TestRunSynthesizeTestBadFlagExits2(t *testing.T) {
	out, code := captureStderrExit(t, func() {
		runSynthesizeTest([]string{"-symbol", "x"})
	})
	if code != 2 {
		t.Fatalf("exit = %d, want 2 for a bad flag", code)
	}
	if n := strings.Count(out, "kern: flags:"); n != 1 {
		t.Fatalf("stderr must contain exactly one \"kern: flags:\" line, got %d:\n%s", n, out)
	}
	if !strings.Contains(out, "flag provided but not defined") {
		t.Fatalf("stderr should name the bad flag:\n%s", out)
	}
	if strings.Contains(out, "Usage of") {
		t.Fatalf("stderr must not contain the stdlib flag usage dump:\n%s", out)
	}
}
