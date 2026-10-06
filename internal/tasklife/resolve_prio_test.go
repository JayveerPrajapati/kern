package tasklife

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/intel"
)

// TestResolveSymbolPrefersProductionOverTest is the I2 regression: a
// multi-word change like "what breaks if I change dispatch" must resolve to
// a PRODUCTION dispatch symbol, never to a test func whose name merely
// shares the request's filler words ("TestWhatIfRequiresChange" matches
// "what if ... change"). The ranked-search fallback must deprioritize
// _test.go symbols the same way intel.ResolveFuzzy does.
func TestResolveSymbolPrefersProductionOverTest(t *testing.T) {
	p := newTestPlatform(t, "testdata/resolve_prio")
	sym, fuzzy, err := resolveSymbol(p, "what breaks if I change dispatch")
	if err != nil {
		t.Fatalf("resolveSymbol: %v", err)
	}
	if !strings.Contains(strings.ToLower(sym), "dispatch") {
		t.Fatalf("resolveSymbol = %q (fuzzy=%v); want a production dispatch symbol", sym, fuzzy)
	}
	if strings.HasPrefix(sym, "Test") {
		t.Fatalf("resolveSymbol = %q; a test func must never win over production symbols", sym)
	}
}

// TestResolveSymbolTestSymbolOnlyFallback pins the last-resort policy: when
// the only resolvable match is a test symbol, it is still used (same
// testFallback contract as intel.ResolveFuzzy) — deprioritization is not
// exclusion.
func TestResolveSymbolTestSymbolOnlyFallback(t *testing.T) {
	p := newTestPlatform(t, "testdata/resolve_prio")
	// "requires" matches only TestWhatIfRequiresChange in the fixture; the
	// extracted candidates ("requires" etc.) do not resolve, so the
	// ranked-search fallback is the only path and the test symbol is its
	// unique match.
	sym, fuzzy, err := resolveSymbol(p, "what if the change requires")
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
// repo root is indexed) instead of the production definition.
func TestResolveSymbolPrefersProductionOverTestdata(t *testing.T) {
	p := newTestPlatform(t, "testdata/resolve_prio")
	sym, fuzzy, err := resolveSymbol(p, "dispatch")
	if err != nil {
		t.Fatalf("resolveSymbol: %v", err)
	}
	f := graphNodeFile(p.Graph(), sym)
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
// and the resolved file is a fixture that testdataFixtureNote flags.
func TestResolveSymbolTestdataOnlyResolvesFixture(t *testing.T) {
	p := newTestPlatform(t, "testdata/resolve_prio")
	sym, _, err := resolveSymbol(p, "stubOnly")
	if err != nil {
		t.Fatalf("resolveSymbol: %v", err)
	}
	f := graphNodeFile(p.Graph(), sym)
	if f == "" {
		t.Fatalf("resolveSymbol(%q) = %q; expected a fixture file", "stubOnly", sym)
	}
	if !intel.IsFixtureFile(f) {
		t.Fatalf("resolveSymbol(%q) file %q; want a testdata fixture file (testdata-only resolution)", "stubOnly", f)
	}
	if note := testdataFixtureNote(p.Graph(), sym); note != "(testdata fixture)" {
		t.Fatalf("testdataFixtureNote(%q) = %q; want %q", sym, note, "(testdata fixture)")
	}
	// A production target carries no annotation.
	if note := testdataFixtureNote(p.Graph(), "dispatch"); note != "" {
		t.Fatalf("testdataFixtureNote(dispatch) = %q; want \"\" for production targets", note)
	}
}
