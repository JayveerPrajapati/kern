package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestRunIndexJSONEmitsSummary pins F-003: `kern index --json` must emit a
// valid JSON summary of the index operation (freshness + symbols/files/
// packages/version/store/languages, mirroring `kern index ensure-fresh
// --json`) instead of silently ignoring the flag and printing human text.
func TestRunIndexJSONEmitsSummary(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)

	out := captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if m["freshness"] != "rebuilt" {
		t.Fatalf("cold index freshness = %v, want rebuilt", m["freshness"])
	}
	if m["action"] != "rebuilt" {
		t.Fatalf("cold index action = %v, want rebuilt", m["action"])
	}
	if m["built"] != true {
		t.Fatalf("index built = %v, want true", m["built"])
	}
	if s, ok := m["symbols"].(float64); !ok || s < 1 {
		t.Fatalf("symbols = %v, want >= 1", m["symbols"])
	}
	if f, ok := m["files"].(float64); !ok || f < 1 {
		t.Fatalf("files = %v, want >= 1", m["files"])
	}
	if _, ok := m["store"].(string); !ok {
		t.Fatalf("store missing from summary: %v", m)
	}
	if _, ok := m["languages"].([]any); !ok {
		t.Fatalf("languages missing from summary: %v", m)
	}
	if _, ok := m["root"].(string); !ok {
		t.Fatalf("root missing from summary: %v", m)
	}
}

// TestRunIndexSkipsRebuildWhenFresh pins F-004 warm-path behavior: the
// second `kern index` on an unchanged tree skips the rebuild and reports the
// fresh state with symbol counts; --force restores the unconditional rebuild.
func TestRunIndexSkipsRebuildWhenFresh(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)

	// Cold: first run builds.
	out := captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if m["freshness"] != "rebuilt" || m["action"] != "rebuilt" {
		t.Fatalf("cold run = %v, want freshness=rebuilt/action=rebuilt", m)
	}
	coldSymbols, _ := m["symbols"].(float64)

	// Warm: same tree, second run must NOT rebuild.
	out = captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m = assertValidJSON(t, out)
	if m["freshness"] != "fresh" || m["action"] != "skipped" {
		t.Fatalf("warm run = %v, want freshness=fresh/action=skipped (no rebuild)", m)
	}
	if m["symbols"] != coldSymbols {
		t.Fatalf("warm symbols = %v, want %v (unchanged)", m["symbols"], coldSymbols)
	}
	if m["stale"] != false {
		t.Fatalf("warm stale = %v, want false", m["stale"])
	}

	// The text output on the warm path reports the fresh state and tells the
	// user how to force a rebuild.
	out = captureStdout(t, func() { runIndex([]string{root}) })
	if !strings.Contains(out, "FRESH") || !strings.Contains(out, "--force") {
		t.Fatalf("warm text output = %q, want FRESH + `--force` hint", out)
	}

	// --force rebuilds unconditionally.
	out = captureStdout(t, func() { runIndex([]string{root, "--json", "--force"}) })
	m = assertValidJSON(t, out)
	if m["freshness"] != "rebuilt" {
		t.Fatalf("forced run = %v, want freshness=rebuilt", m)
	}
}

// TestRunIndexRebuildsWhenStale pins warm-path invalidation: a content edit
// makes the persisted index stale, so the next `kern index` rebuilds and the
// JSON summary reports the new counts.
func TestRunIndexRebuildsWhenStale(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)

	runIndex([]string{root, "--json"})

	// Adding a file changes the content root: the index is now stale.
	if err := os.WriteFile(filepath.Join(root, "extra.go"), []byte("package main\n\nfunc extra() {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	out := captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if m["freshness"] != "rebuilt" {
		t.Fatalf("stale run = %v, want freshness=rebuilt", m)
	}
	if s, ok := m["symbols"].(float64); !ok || s < 2 {
		t.Fatalf("symbols = %v, want >= 2 after adding extra.go", m["symbols"])
	}
}

// TestRunIndexUpdateJSONEmitsSummary pins the update-path JSON fix: `kern
// index --update --json` must emit the same valid JSON summary the plain
// `--json` path emits (with the update-specific `reused` count) instead of
// always printing the human "index updated: ..." text line.
func TestRunIndexUpdateJSONEmitsSummary(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := jsonCliFixture(t)

	// Cold build first so --update has a previous index to update against.
	out := captureStdout(t, func() { runIndex([]string{root, "--json"}) })
	m := assertValidJSON(t, out)
	if m["action"] != "rebuilt" {
		t.Fatalf("cold run = %v, want action=rebuilt", m)
	}

	// --update --json must emit JSON, not the human text line.
	out = captureStdout(t, func() { runIndex([]string{root, "--update", "--json"}) })
	m = assertValidJSON(t, out)
	if m["action"] != "updated" {
		t.Fatalf("update run action = %v, want updated", m["action"])
	}
	if m["built"] != true {
		t.Fatalf("update run built = %v, want true", m["built"])
	}
	if m["stale"] != false {
		t.Fatalf("update run stale = %v, want false", m["stale"])
	}
	if s, ok := m["symbols"].(float64); !ok || s < 1 {
		t.Fatalf("symbols = %v, want >= 1", m["symbols"])
	}
	if f, ok := m["files"].(float64); !ok || f < 1 {
		t.Fatalf("files = %v, want >= 1", m["files"])
	}
	if _, ok := m["reused"].(float64); !ok {
		t.Fatalf("reused missing from update summary: %v", m)
	}
	if _, ok := m["store"].(string); !ok {
		t.Fatalf("store missing from update summary: %v", m)
	}
}
