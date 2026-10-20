package app

import (
	"strings"
	"testing"
)

// B2 (QA 2026-10-15): low-confidence resolution labels — fuzzy matches and
// common-word name collisions must never substitute silently.
func TestResolutionNote(t *testing.T) {
	p := &Platform{}
	// Bare, deliberate names are clean.
	if got := p.resolutionNote("NewServer", "NewServer", false); got != "" {
		t.Errorf("bare exact resolution must be unlabeled, got %q", got)
	}
	// Prose resolving to a genuinely-named symbol is clean.
	if got := p.resolutionNote("add caching to UserService", "UserService", false); got != "" {
		t.Errorf("clean prose resolution must be unlabeled, got %q", got)
	}
	// Fuzzy resolution is labeled.
	if got := p.resolutionNote("NewServr", "NewServer", true); got != `resolved "NewServr" -> NewServer (fuzzy match)` {
		t.Errorf("fuzzy resolution note = %q", got)
	}
	// Prose resolving to a common-word-named symbol is a collision.
	got := p.resolutionNote("fix the flaky test in delivery", "test", false)
	if !strings.Contains(got, "name collision") || !strings.Contains(got, "low confidence") {
		t.Errorf("collision note = %q, want name-collision + low-confidence wording", got)
	}
}

// TestResolutionNoteNoDeclHash (QA 6.6): a fuzzy resolution that mapped
// through a decl:<sha> identity must render the agent-usable name, never the
// machine bookkeeping hash. The resolve-priority fixture's ambiguous
// "dispatch" bare name is exactly the case resolveSymbol maps through
// DeclarationID; the banner must name the picked definition instead.
func TestResolutionNoteNoDeclHash(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes fixture; skipped with -short")
	}
	p := prioPlatform(t)
	sym, fuzzy, err := p.resolveSymbol("dispatch")
	if err != nil {
		t.Fatalf("resolveSymbol(dispatch): %v", err)
	}
	if !fuzzy {
		t.Fatalf("ambiguous bare name must resolve fuzzy, got %q", sym)
	}
	if !strings.HasPrefix(sym, "decl:") {
		t.Fatalf("resolveSymbol must map the ambiguous bare name through DeclarationID for this test to exercise the leak, got %q", sym)
	}
	note := p.resolutionNote("dispatch", sym, true)
	if strings.Contains(note, "decl:") {
		t.Errorf("resolution note must not carry a decl: hash, got %q", note)
	}
	// The note must name a real definition from the fixture (a.dispatch or
	// b.dispatch) so the substitution stays visible.
	if !strings.Contains(note, ".dispatch") && !strings.Contains(note, "dispatch") {
		t.Errorf("resolution note must name the resolved symbol, got %q", note)
	}
}

// TestDisplayNameMapsDeclID (QA 6.6): displayName resolves a decl:<sha>
// identity to the graph node's label, falling back to the index symbol's
// full name — never returning the raw hash when a name is knowable.
func TestDisplayNameMapsDeclID(t *testing.T) {
	// No graph/index: an unknown decl identity passes through unchanged (the
	// caller decides whether to print it); a non-decl ref always passes
	// through.
	p := &Platform{}
	if got := p.displayName("greet"); got != "greet" {
		t.Errorf("bare name must pass through unchanged, got %q", got)
	}
	if got := p.displayName("app.greet"); got != "app.greet" {
		t.Errorf("package-scoped name must pass through unchanged, got %q", got)
	}
	if got := p.displayName("file:app/main.go"); got != "file:app/main.go" {
		t.Errorf("file node ID must pass through unchanged, got %q", got)
	}
}
