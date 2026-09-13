package mcp

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/retrieval"
)

// TestHandleContextLens drives the P1-002 lens arg through kern_context: a
// valid lens prepends the deterministic "lens: name (type=weight, ...)" line;
// an unknown lens is rejected; no lens arg leaves the output byte-identical.
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

// TestHandleContextProfile drives the P1-005 profile arg through kern_context:
// machine-json wraps the body in a JSON envelope; an unknown profile is
// rejected; no profile arg leaves the output byte-identical.
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

// TestHandleContextLevel drives the P2 optional level arg through kern_context:
// L1 renders the names/token-cost list, L2 the neighborhood packet, L3 the
// source slice; level matching is case-insensitive; an invalid level is
// rejected with the documented wording; no level arg leaves the output
// byte-identical (asserted via the base call above).
func TestHandleContextLevel(t *testing.T) {
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

	l1, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "level": "l1"})
	if err != nil {
		t.Fatalf("handleContext level l1: %v", err)
	}
	if !strings.Contains(l1, "== level 1:") || !strings.Contains(l1, "Greet") {
		t.Errorf("l1 output missing level-1 names/token-cost list: %q", l1)
	}

	l2, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "level": "l2"})
	if err != nil {
		t.Fatalf("handleContext level l2: %v", err)
	}
	if !strings.Contains(l2, "== level 2: Greet ==") {
		t.Errorf("l2 output missing neighborhood header: %q", l2)
	}

	l3, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "level": "L3"})
	if err != nil {
		t.Fatalf("handleContext level l3 (upper): %v", err)
	}
	if !strings.Contains(l3, "== level 3: Greet ==") {
		t.Errorf("l3 output missing source header: %q", l3)
	}

	if _, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "level": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown level") {
		t.Errorf("invalid level: err = %v, want rejection with 'unknown level'", err)
	}
}

// TestHandleContextHandle drives the P2 optional handle arg through
// kern_context: a registry-resolved handle renders the symbol's L2
// neighborhood instead of the default context slice; the 8-char prefix
// fallback (kern_retrieve renders prefixes) resolves; an unknown handle is
// rejected with the kern_resolve wording; handle+level are mutually
// exclusive.
func TestHandleContextHandle(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	// Register a handle for Greet in the package-level registry, the way
	// kern_retrieve does after a retrieval.
	h := retrieval.NewHandle(retrieval.TypeSymbol, "Greet", "app.go", 3, 40, 1.0, "hash")
	retrieval.DefaultRegistry.Register(h)
	prefix := h.ID[:8]

	out, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "handle": prefix})
	if err != nil {
		t.Fatalf("handleContext with handle: %v", err)
	}
	if !strings.Contains(out, "== level 2: Greet ==") {
		t.Errorf("handle output missing level-2 neighborhood header: %q", out)
	}

	if _, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "handle": "deadbeef00"}); err == nil || !strings.Contains(err.Error(), "unknown handle") {
		t.Errorf("unknown handle: err = %v, want rejection with 'unknown handle'", err)
	}

	if _, err := s.handleContext(context.Background(), map[string]any{"root": root, "symbol": "Greet", "handle": prefix, "level": "l2"}); err == nil || !strings.Contains(err.Error(), "use only one of handle/level") {
		t.Errorf("handle+level: err = %v, want 'use only one of handle/level'", err)
	}
}

// TestHandleExploreLevel drives the P2 optional level arg through kern_explore:
// L1/L2/L3 render the symbol via the retrieval levels instead of the explore
// report; an invalid level is rejected; default behavior is unchanged (base
// call above succeeds with the explore render).
func TestHandleExploreLevel(t *testing.T) {
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

	l2, err := s.handleExplore(context.Background(), map[string]any{"root": root, "symbol": "Greet", "level": "l2"})
	if err != nil {
		t.Fatalf("handleExplore level l2: %v", err)
	}
	if !strings.Contains(l2, "== level 2: Greet ==") {
		t.Errorf("l2 output missing neighborhood header: %q", l2)
	}

	if _, err := s.handleExplore(context.Background(), map[string]any{"root": root, "symbol": "Greet", "level": "bogus"}); err == nil || !strings.Contains(err.Error(), "unknown level") {
		t.Errorf("invalid level: err = %v, want rejection with 'unknown level'", err)
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

// TestHandleProbeLevel drives the P2 optional level arg through kern_probe:
// the primary probed symbol renders through the retrieval levels instead of
// the probe report; a task with no resolvable symbol is rejected.
func TestHandleProbeLevel(t *testing.T) {
	root := provenanceProject(t)
	s := NewServer(strings.NewReader(""), io.Discard)
	defer s.Close()

	out, err := s.handleProbe(context.Background(), map[string]any{"root": root, "task": "what does Greet touch", "level": "l3"})
	if err != nil {
		t.Fatalf("handleProbe level l3: %v", err)
	}
	if !strings.Contains(out, "== level 3:") {
		t.Errorf("l3 output missing source header: %q", out)
	}

	if _, err := s.handleProbe(context.Background(), map[string]any{"root": root, "task": "zzzzqqqq xxxxxx", "level": "l1"}); err == nil || !strings.Contains(err.Error(), "no symbol resolved") {
		t.Errorf("no-symbol task: err = %v, want 'no symbol resolved'", err)
	}
}
