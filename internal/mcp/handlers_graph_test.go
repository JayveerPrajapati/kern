package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestHandleContextLens(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	// Drain the session's background index save before TempDir cleanup so the
	// .kern persistence goroutine cannot race the RemoveAll (pre-existing
	// flake: "TempDir RemoveAll cleanup: directory not empty").
	defer s.Close()

	base, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet"})
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if base == "" {
		t.Fatal("handleContext returned empty body")
	}

	lensed, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "lens": "security"})
	if err != nil {
		t.Fatalf("handleContext with lens: %v", err)
	}
	if !strings.HasPrefix(lensed, "lens: security (") {
		t.Errorf("lensed output = %q, want 'lens: security (' prefix", lensed)
	}
	if !strings.HasSuffix(lensed, base) {
		t.Error("lensed output should keep the original body after the lens line")
	}

	if _, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "lens": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown lens") {
		t.Errorf("unknown lens: err = %v, want rejection with 'unknown lens'", err)
	}
}

func TestHandleContextProfile(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	base, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet"})
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	// No profile arg -> byte-identical to the no-profile case.
	same, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "profile": ""})
	if err != nil || same != base {
		t.Errorf("empty profile arg should be byte-identical, err=%v", err)
	}

	js, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "profile": "machine-json"})
	if err != nil {
		t.Fatalf("handleContext machine-json: %v", err)
	}
	var m struct {
		Profile string `json:"profile"`
		Format  string `json:"format"`
		Content string `json:"content"`
	}
	if err := json.Unmarshal([]byte(js), &m); err != nil {
		t.Fatalf("machine-json output not valid JSON: %v\n%s", err, js)
	}
	if m.Profile != "machine-json" || m.Format != "json" || m.Content != base {
		t.Errorf("decoded profile=%q format=%q content-match=%v; want machine-json/json/original body",
			m.Profile, m.Format, m.Content == base)
	}

	if _, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "profile": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown profile") {
		t.Errorf("unknown profile: err = %v, want rejection with 'unknown profile'", err)
	}
}

// TestHandleContextRejectsLevel pins the T2a contract: the level retrieval
// path was removed from kern_context (progressive-disclosure level views are
// served exclusively by kern_retrieve), so a level arg must be rejected with
// the kern_retrieve hint instead of silently falling back to the context
// slice. The default (no level arg) behavior is unchanged.
func TestHandleContextRejectsLevel(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	base, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet"})
	if err != nil {
		t.Fatalf("handleContext: %v", err)
	}
	if base == "" {
		t.Fatal("handleContext returned empty body")
	}

	if _, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "level": "l1"}); err == nil || !strings.Contains(err.Error(), "kern_retrieve") {
		t.Errorf("level arg: err = %v, want rejection pointing at kern_retrieve", err)
	}
}

// TestHandleContextRejectsHandle pins the T2a contract: the handle retrieval
// path (mirroring kern_resolve) was removed from kern_context, so a handle
// arg must be rejected with the kern_retrieve hint instead of silently
// falling back to the context slice.
func TestHandleContextRejectsHandle(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	if _, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "handle": "deadbeef00"}); err == nil || !strings.Contains(err.Error(), "kern_retrieve") {
		t.Errorf("handle arg: err = %v, want rejection pointing at kern_retrieve", err)
	}
}

// TestHandleExploreRejectsLevel pins the T2a contract: the level retrieval
// path was removed from kern_explore (progressive-disclosure level views are
// served exclusively by kern_retrieve), so a level arg must be rejected with
// the kern_retrieve hint instead of silently falling back to the explore
// report. The default (no level arg) behavior is unchanged.
func TestHandleExploreRejectsLevel(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	base, err := s.handleExplore(context.Background(), map[string]any{"root": root, "symbol": "Greet"})
	if err != nil {
		t.Fatalf("handleExplore: %v", err)
	}
	if base == "" {
		t.Fatal("handleExplore returned empty body")
	}

	if _, err := s.handleExplore(context.Background(), map[string]any{"root": root, "symbol": "Greet", "level": "l2"}); err == nil || !strings.Contains(err.Error(), "kern_retrieve") {
		t.Errorf("level arg: err = %v, want rejection pointing at kern_retrieve", err)
	}
}

// TestHandleProbeRejectsLevel pins the T2a contract: the level retrieval
// path was removed from kern_probe (progressive-disclosure level views are
// served exclusively by kern_retrieve), so a level arg must be rejected with
// the kern_retrieve hint instead of silently falling back to the probe
// report. The default (no level arg) behavior is unchanged.
func TestHandleProbeRejectsLevel(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	out, err := s.handleProbe(context.Background(), map[string]any{"root": root, "task": "what does Greet touch"})
	if err != nil {
		t.Fatalf("handleProbe: %v", err)
	}
	if out == "" {
		t.Fatal("handleProbe returned empty body")
	}

	if _, err := s.handleProbe(context.Background(), map[string]any{"root": root, "task": "what does Greet touch", "level": "l3"}); err == nil || !strings.Contains(err.Error(), "kern_retrieve") {
		t.Errorf("level arg: err = %v, want rejection pointing at kern_retrieve", err)
	}
}

// chainProject builds a 5-deep caller chain Top→L3→L2→L1→L0: exploring L0
// puts L3/Top at radius depth 3/4, beyond the P2-8 default depth of 2.
func chainProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package chain

func L0() {}
func L1() { L0() }
func L2() { L1() }
func L3() { L2() }
func Top() { L3() }
`
	if err := os.WriteFile(filepath.Join(root, "chain.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	return root
}

// TestHandleExploreDefaultBounds pins the P2-8 promotion defaults on the MCP
// path: an unadorned call caps the radius at 2 hops, while an explicit
// depth=0 keeps the uncapped radius.
func TestHandleExploreDefaultBounds(t *testing.T) {
	root := chainProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	def, err := s.handleExplore(context.Background(), map[string]any{"root": root, "symbol": "L0"})
	if err != nil {
		t.Fatalf("handleExplore: %v", err)
	}
	// Scope radius assertions to the blast-radius section: the verbatim
	// source block legitimately names every function in the file.
	radiusOf := func(out string) string {
		start := strings.Index(out, "== blast radius")
		end := strings.Index(out, "== affected files")
		if start < 0 || end < 0 || end <= start {
			t.Fatalf("explore output missing radius sections:\n%s", out)
		}
		return out[start:end]
	}
	defRadius := radiusOf(def)
	for _, want := range []string{"L1", "L2"} {
		if !strings.Contains(defRadius, want) {
			t.Errorf("default radius missing %q:\n%s", want, defRadius)
		}
	}
	if !strings.Contains(def, "tokens:") {
		t.Errorf("default explore missing always-on savings panel:\n%s", def)
	}
	if strings.Contains(defRadius, "Top") {
		t.Errorf("default depth=2 must exclude depth-4 Top:\n%s", defRadius)
	}
	full, err := s.handleExplore(context.Background(), map[string]any{"root": root, "symbol": "L0", "depth": "0", "max": "0"})
	if err != nil {
		t.Fatalf("handleExplore depth=0: %v", err)
	}
	if !strings.Contains(radiusOf(full), "Top") {
		t.Errorf("explicit depth=0 must keep the uncapped radius:\n%s", full)
	}
}

// TestHandleExploreExplain pins P2-8 explain on the MCP path: explain=true
// appends the why-rationale section to the explore report.
func TestHandleExploreExplain(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	out, err := s.handleExplore(context.Background(), map[string]any{"root": root, "symbol": "Greet", "explain": "true"})
	if err != nil {
		t.Fatalf("handleExplore explain: %v", err)
	}
	for _, want := range []string{"== why ==", "who depends on it and why", "Greet"} {
		if !strings.Contains(out, want) {
			t.Errorf("explain output missing %q:\n%s", want, out)
		}
	}
}
