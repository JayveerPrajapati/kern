package app

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// resolvePrioFixture writes the resolve-priority fixture mirroring the
// tasklife copy (internal/tasklife/testdata/resolve_prio): production
// dispatch definitions (prod/, a/, b/), a testdata fixture dispatch
// (testdata/), a testdata-only stubOnly, and a TestWhatIfRequiresChange test
// func whose name shares the filler words of the I2 repro query. On top of
// the tasklife fixture it adds two symbols the app copy's ranked-search
// fallback can actually reach: the app copy lacks the tasklife
// candidate-refinement loop, and a bare `dispatch` scores only 100 (< the
// 150 ranked-search gate) on prose queries — only a symbol matching the
// query's words scores >= 150. prod/ChangeDispatch (production) and
// testdata/WhatIfChangeDispatch (fixture) both match the shared "what if
// change dispatch" words, so the ranked loop must prefer the production one.
func resolvePrioFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod": "module resolveprio\n\ngo 1.25\n",
		"prod_test.go": `package main

import "testing"

// TestWhatIfRequiresChange is a test func whose name shares the filler
// words ("what if ... change") of the I2 repro query; it must never win
// resolution over the production dispatch symbols.
func TestWhatIfRequiresChange(t *testing.T) {}
`,
		"prod/dispatch.go": `// Package prod is the production side of the resolve-priority fixture (Fix 3):
// a name defined here AND under testdata/ must resolve to this definition.
package prod

func dispatch() {}
`,
		"prod/changedispatch.go": `package prod

// ChangeDispatch is the production symbol the ranked-search fallback must
// prefer: it shares the "what if change dispatch" query words with the
// testdata fixture WhatIfChangeDispatch and the test func
// TestWhatIfRequiresChange, but is the only non-test, non-fixture hit.
func ChangeDispatch() {}
`,
		"testdata/dispatch.go": `// Package testdata is the fixture-stub side of the resolve-priority fixture
// (Fix 3): this dispatch must never win over the production definition.
package testdata

func dispatch() {}
`,
		"testdata/changedispatch.go": `package testdata

// WhatIfChangeDispatch is the fixture-stub side of the ranked-search
// production preference (Fix 3): it outranks the production ChangeDispatch
// on the "what if change dispatch" query, so it must be demoted to the
// testFallback and never win resolution.
func WhatIfChangeDispatch() {}
`,
		"testdata/stubonly.go": `package testdata

// stubOnly exists ONLY as a testdata fixture — resolution must still succeed
// (resolve but annotate) and its file must be recognized as a fixture.
func stubOnly() {}
`,
		"a/a.go": `// Package a owns one side of the ambiguous "dispatch" bare name.
package a

// dispatch routes a request to its handler.
func dispatch() {}
`,
		"b/b.go": `// Package b owns the other side of the ambiguous "dispatch" bare name.
package b

// dispatch routes a request to its handler.
func dispatch() {}
`,
	}
	for rel, content := range files {
		p := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// prioPlatform builds a *Platform over the resolve-priority fixture the same
// way the other internal/app tests do (index.Build + NewWithIndex).
func prioPlatform(t *testing.T) *Platform {
	t.Helper()
	if testing.Short() {
		t.Skip("indexes fixture; skipped with -short")
	}
	root := resolvePrioFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	p, err := NewWithIndex(root, ix)
	if err != nil {
		t.Fatalf("NewWithIndex: %v", err)
	}
	return p
}

// fixtureNote mirrors the tasklife testdataFixtureNote (Fix 3 "resolve but
// annotate" contract): returns "(testdata fixture)" when target's defining
// file is a testdata fixture, and "" for production targets.
func fixtureNote(p *Platform, target string) string {
	if target == "" {
		return ""
	}
	f := p.graphNodeFile(target)
	if f == "" || !intel.IsFixtureFile(f) {
		return ""
	}
	return "(testdata fixture)"
}

// TestResolveSymbolPrefersProductionOverTest is the I2 regression: a
// multi-word change like "what breaks if I change dispatch" must resolve to
// a PRODUCTION dispatch symbol, never to a test func (or testdata fixture)
// whose name merely shares the request's words. The tasklife copy resolves
// this via its candidate-refinement loop; the app copy reaches the same
// outcome through the ranked-search fallback, which must deprioritize
// _test.go/testdata symbols the same way intel.ResolveFuzzy does.
func TestResolveSymbolPrefersProductionOverTest(t *testing.T) {
	p := prioPlatform(t)
	sym, fuzzy, err := p.resolveSymbol("what breaks if I change dispatch")
	if err != nil {
		t.Fatalf("resolveSymbol: %v", err)
	}
	if !strings.Contains(strings.ToLower(sym), "dispatch") {
		t.Fatalf("resolveSymbol = %q (fuzzy=%v); want a production dispatch symbol", sym, fuzzy)
	}
	if strings.HasPrefix(sym, "Test") {
		t.Fatalf("resolveSymbol = %q; a test func must never win over production symbols", sym)
	}
	if f := p.graphNodeFile(sym); intel.IsFixtureFile(f) {
		t.Fatalf("resolveSymbol = %q -> file %q; a testdata fixture must never win over production symbols", sym, f)
	}
}

// TestResolveSymbolTestSymbolOnlyFallback pins the last-resort policy: when
// the only resolvable match is a test symbol, it is still used (same
// testFallback contract as intel.ResolveFuzzy) — deprioritization is not
// exclusion.
func TestResolveSymbolTestSymbolOnlyFallback(t *testing.T) {
	p := prioPlatform(t)
	// "requires" matches only TestWhatIfRequiresChange in the fixture; the
	// extracted candidates ("requires" etc.) do not resolve, so the
	// ranked-search fallback is the only path and the test symbol is its
	// unique match.
	sym, fuzzy, err := p.resolveSymbol("what if the change requires")
	if err != nil {
		t.Fatalf("resolveSymbol: %v", err)
	}
	if sym != "TestWhatIfRequiresChange" {
		t.Fatalf("resolveSymbol = %q (fuzzy=%v); want the unique test-symbol match TestWhatIfRequiresChange", sym, fuzzy)
	}
	if !fuzzy {
		t.Fatalf("resolveSymbol = %q; expected fuzzy=true for the ranked fallback", sym)
	}
}

// TestResolveSymbolPrefersProductionOverTestdata pins Fix 3: a name defined
// in BOTH production and a testdata fixture must resolve to the production
// definition, never to the fixture stub. The live defect was "dispatch"
// resolving to the stub in testdata/resolve_prio/a/a.go (a fixture when the
// repo root is indexed) instead of the production definition. Adapted to the
// app copy: the testdata fixture WhatIfChangeDispatch outranks the
// production ChangeDispatch on the ranked search, so the fallback must
// return the production definition.
func TestResolveSymbolPrefersProductionOverTestdata(t *testing.T) {
	p := prioPlatform(t)
	sym, fuzzy, err := p.resolveSymbol("what if change dispatch")
	if err != nil {
		t.Fatalf("resolveSymbol: %v", err)
	}
	f := p.graphNodeFile(sym)
	if f == "" {
		t.Fatalf("resolveSymbol = %q; expected a resolvable production definition", sym)
	}
	if intel.IsFixtureFile(f) {
		t.Fatalf("resolveSymbol = %q -> file %q; a testdata fixture stub must never win over production (fuzzy=%v)", sym, f, fuzzy)
	}
}

// TestResolveSymbolTestdataOnlyResolvesFixture pins the "resolve but
// annotate" half of Fix 3: when the ONLY definition of a name is a testdata
// fixture, resolution still succeeds — deprioritization is not exclusion —
// and the resolved file is a fixture that fixtureNote flags.
func TestResolveSymbolTestdataOnlyResolvesFixture(t *testing.T) {
	p := prioPlatform(t)
	sym, _, err := p.resolveSymbol("stubOnly")
	if err != nil {
		t.Fatalf("resolveSymbol: %v", err)
	}
	f := p.graphNodeFile(sym)
	if f == "" {
		t.Fatalf("resolveSymbol(%q) = %q; expected a fixture file", "stubOnly", sym)
	}
	if !intel.IsFixtureFile(f) {
		t.Fatalf("resolveSymbol(%q) file %q; want a testdata fixture file (testdata-only resolution)", "stubOnly", f)
	}
	if note := fixtureNote(p, sym); note != "(testdata fixture)" {
		t.Fatalf("fixtureNote(%q) = %q; want %q", sym, note, "(testdata fixture)")
	}
	// A production target carries no annotation.
	if note := fixtureNote(p, "dispatch"); note != "" {
		t.Fatalf("fixtureNote(dispatch) = %q; want \"\" for production targets", note)
	}
}
