package intel

import (
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// neverMethodsScopeIndex builds the A5 shapes (Phase 5 Part C, 2026-10-06):
//
//   - Ruby: the action.yml file-owned edge calls the homebrew Kern.install
//     METHOD with a receiver-less callee endpoint ("install") — the exact
//     live shape the language-agnostic never-methods rule was measured to
//     drop (4 live edges: file:action.yml / the two .github/actions
//     action.yml files / TestUpdateE2E -> install, all resolving to
//     homebrew.Kern.install).
//   - Go: CheckBoundariesPrecise's bare check() is NOT a call to the
//     Schema.check method — the Go-scoping case the never-methods rule
//     exists for (a bare call binds to a package-level func or the builtin).
//   - TypeScript: LoudGreeter.shout calls this.greet(), a method inherited
//     from the base Greeter class — the A5 inherited-method shape the
//     fixture corpus now exercises (the receiver-less edge is recorded bare
//     "greet"; the receiver-qualified caller is rescued by the
//     receiver-matched resolution path before the never-methods rule).
func neverMethodsScopeIndex() *index.Index {
	return &index.Index{
		Root: "/a5",
		Symbols: []index.Symbol{
			{Kind: "type", Name: "Kern", File: "homebrew/kern.rb", Line: 1, Lang: "ruby"},
			{Kind: "method", Name: "install", Receiver: "Kern", File: "homebrew/kern.rb", Line: 2, Lang: "ruby"},
			{Kind: "type", Name: "Schema", File: "internal/schema/schema.go", Line: 1, Lang: "go"},
			{Kind: "method", Name: "check", Receiver: "Schema", File: "internal/schema/schema.go", Line: 2, Lang: "go"},
			{Kind: "func", Name: "CheckBoundariesPrecise", File: "internal/boundaries/check.go", Line: 1, Lang: "go"},
			{Kind: "type", Name: "Greeter", File: "testfixture/fixtures.ts", Line: 9, Lang: "typescript"},
			{Kind: "method", Name: "greet", Receiver: "Greeter", File: "testfixture/fixtures.ts", Line: 16, Lang: "typescript"},
			{Kind: "type", Name: "LoudGreeter", File: "testfixture/fixtures.ts", Line: 44, Lang: "typescript"},
			{Kind: "method", Name: "shout", Receiver: "LoudGreeter", File: "testfixture/fixtures.ts", Line: 45, Lang: "typescript"},
		},
		Calls: map[string][]index.CallEdge{
			"file:action.yml":        {{Target: "install", Confidence: index.ConfidenceHigh}},
			"CheckBoundariesPrecise": {{Target: "check", Confidence: index.ConfidenceHigh}},
			"LoudGreeter.shout":      {{Target: "greet", Confidence: index.ConfidenceHigh}},
		},
		Callers: map[string][]string{
			"install": {"file:action.yml"},
			"check":   {"CheckBoundariesPrecise"},
			"greet":   {"LoudGreeter.shout"},
		},
		Pkgs: map[string]*index.Pkg{
			"homebrew":            {Name: "homebrew", Path: "homebrew", Files: []string{"homebrew/kern.rb"}, Lang: "ruby"},
			"internal/schema":     {Name: "schema", Path: "internal/schema", Files: []string{"internal/schema/schema.go"}, Lang: "go"},
			"internal/boundaries": {Name: "boundaries", Path: "internal/boundaries", Files: []string{"internal/boundaries/check.go"}, Lang: "go"},
			"testfixture":         {Name: "testfixture", Path: "testfixture", Files: []string{"testfixture/fixtures.ts"}, Lang: "typescript"},
		},
	}
}

// TestNeverMethodsGuardIsGoScoped pins the A5 verdict: the bare-callee
// never-methods rule in calleeIsTarget has Go-only rationale (Go scoping: a
// bare reference binds to a package-level func or the builtin, never a
// method) and is now gated to Go symbols via the resolved callee's language.
// A foreign receiver-less method edge (Ruby install, TS this.greet()) must
// survive; the Go rule must still fire (Schema.check). Verified live before
// the fix: 4 foreign edges were dropped (see the index comment above); the
// per-language guard model in internal/index/parity_report_test.go renders
// the same contract for the fixture corpus.
func TestNeverMethodsGuardIsGoScoped(t *testing.T) {
	g := FromIndex(neverMethodsScopeIndex())

	// Ruby: the file-owned receiver-less edge to the Kern.install method is a
	// real callee — the guard must not drop it (was dropped pre-A5).
	callers := g.DirectCallersNames("homebrew.Kern.install", false)
	if !containsID(callers, "file:action.yml") {
		t.Errorf("DirectCallersNames(homebrew.Kern.install) = %v; foreign bare-callee method edge must survive the never-methods guard (A5)", callers)
	}

	// TypeScript: the inherited this.greet() edge (LoudGreeter.shout ->
	// Greeter.greet) survives — receiver-qualified caller rescued by the
	// receiver-matched path.
	callers = g.DirectCallersNames("testfixture.Greeter.greet", false)
	if !containsID(callers, "LoudGreeter.shout") {
		t.Errorf("DirectCallersNames(testfixture.Greeter.greet) = %v; TS inherited this.greet() edge must survive (A5)", callers)
	}

	// Go: the never-methods rule still fires — CheckBoundariesPrecise's bare
	// check() is not a call to the Schema.check method.
	callers = g.DirectCallersNames("internal/schema.Schema.check", false)
	if containsID(callers, "CheckBoundariesPrecise") {
		t.Errorf("DirectCallersNames(internal/schema.Schema.check) = %v; Go bare call must not be attributed to the method (never-methods rule)", callers)
	}
}
