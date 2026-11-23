package typeimpact

import (
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestTypeImpactRootsExpandsMethodsAndConstructors pins the P0 fix: a type
// query must expand to the symbols that actually carry call edges. Before the
// fix, `kern impact FileStore` reported "0 callers, risk low".
func TestTypeImpactRootsExpandsMethodsAndConstructors(t *testing.T) {
	symbols := []index.Symbol{
		{Kind: "struct", Name: "Store", File: "pkg/store.go", Line: 1, Lang: "go"},
		{Kind: "method", Name: "Get", Receiver: "Store", File: "pkg/store.go", Line: 10, Lang: "go"},
		{Kind: "method", Name: "Put", Receiver: "Store", File: "pkg/store.go", Line: 20, Lang: "go"},
		{Kind: "func", Name: "NewStore", File: "pkg/store.go", Line: 30, Lang: "go", Returns: []string{"*Store"}},
		// Same receiver name in another package must NOT be pulled in.
		{Kind: "method", Name: "Get", Receiver: "Store", File: "other/store.go", Line: 10, Lang: "go"},
	}
	roots, exp, ok := TypeImpactRoots(&symbols[0], symbols)
	if !ok {
		t.Fatal("Store is a struct; expansion must apply")
	}
	if len(roots) != 4 {
		t.Fatalf("roots = %v, want type + 2 methods + 1 constructor", roots)
	}
	joined := strings.Join(roots, ",")
	for _, want := range []string{"Store", "Store.Get", "Store.Put", "NewStore"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("roots missing %q: %v", want, roots)
		}
	}
	if strings.Count(joined, "Store.Get") != 1 {
		t.Fatalf("cross-directory method leaked into roots: %v", roots)
	}
	if exp != "Store → 2 methods, 1 constructors" {
		t.Fatalf("expansion = %q", exp)
	}
}

// TestTypeImpactRootsLeavesFunctionsAlone guards against expanding ordinary
// symbols (the control: a function keeps its exact single-root report).
func TestTypeImpactRootsLeavesFunctionsAlone(t *testing.T) {
	fn := index.Symbol{Kind: "func", Name: "Run", File: "main.go", Line: 1, Lang: "go"}
	if roots, exp, ok := TypeImpactRoots(&fn, []index.Symbol{fn}); ok || len(roots) != 0 || exp != "" {
		t.Fatalf("function must not expand: roots=%v exp=%q ok=%v", roots, exp, ok)
	}
}

// TestTypeImpactRootsWithIndexSeesThroughAlias pins the audit fix: a query on
// `type A = B` must carry B's methods and constructors, and the expansion must
// name the aliased package so the report is never ambiguous.
func TestTypeImpactRootsWithIndexSeesThroughAlias(t *testing.T) {
	symbols := []index.Symbol{
		{Kind: "struct", Name: "Thing", File: "internal/base/base.go", Line: 3, Lang: "go"},
		{Kind: "method", Name: "Do", Receiver: "Thing", File: "internal/base/base.go", Line: 5, Lang: "go"},
		{Kind: "func", Name: "NewThing", File: "internal/base/base.go", Line: 9, Lang: "go"},
		{Kind: "type", Name: "T", Alias: "base.Thing", File: "internal/alias/a.go", Line: 5, Lang: "go"},
	}
	ix := &index.Index{Root: "/kern", Symbols: symbols, Pkgs: map[string]*index.Pkg{
		"internal/base":  {Name: "base", Path: "internal/base", Lang: "go"},
		"internal/alias": {Name: "alias", Path: "internal/alias", Lang: "go"},
	}}
	roots, exp, ok := TypeImpactRootsWithIndex(ix, &ix.Symbols[3])
	if !ok {
		t.Fatal("alias type not recognized")
	}
	want := map[string]bool{"T": true, "Thing.Do": true, "NewThing": true}
	for _, r := range roots {
		delete(want, r)
	}
	if len(want) > 0 {
		t.Fatalf("alias expansion missed roots %v (got %v)", want, roots)
	}
	if !strings.Contains(exp, "alias of base.Thing") {
		t.Fatalf("expansion = %q; want it to name the aliased package", exp)
	}
}

// TestTypeImpactRootsMethodlessTypeStillHandled pins that a type with no
// methods or constructors is still handled (roots=[type], ok=true, empty
// expansion) — the caller can then apply the reference-unknown safety net
// instead of treating it as an unattributable kind.
func TestTypeImpactRootsMethodlessTypeStillHandled(t *testing.T) {
	d := index.Symbol{Kind: "struct", Name: "Bare", File: "pkg/bare.go", Line: 1, Lang: "go"}
	roots, exp, ok := TypeImpactRoots(&d, []index.Symbol{d})
	if !ok {
		t.Fatal("type must be handled even with no methods/constructors")
	}
	if len(roots) != 1 || roots[0] != "Bare" {
		t.Fatalf("roots = %v, want [Bare]", roots)
	}
	if exp != "" {
		t.Fatalf("expansion = %q, want empty for a methodless type", exp)
	}
}

// TestIsTypeKindPinsUnattributableKinds pins exactly which declaration kinds
// are types (aggregated) vs which are unattributable and must fall back to
// the reference safety net.
func TestIsTypeKindPinsUnattributableKinds(t *testing.T) {
	for _, kind := range []string{"struct", "interface", "class", "type", "enum", "record"} {
		if !IsTypeKind(kind) {
			t.Errorf("IsTypeKind(%q) = false, want true", kind)
		}
	}
	for _, kind := range []string{"func", "method", "const", "var", "field", ""} {
		if IsTypeKind(kind) {
			t.Errorf("IsTypeKind(%q) = true, want false", kind)
		}
	}
}

// TestReturnsTypeStripsDecoration pins constructor detection across pointer,
// package-qualified and slice return types.
func TestReturnsTypeStripsDecoration(t *testing.T) {
	if !ReturnsType([]string{"*FileStore", "pkg.FileStore", "[]*FileStore"}, "FileStore") {
		t.Fatal("ReturnsType must strip pointer/qualifier/slice decoration")
	}
	if ReturnsType([]string{"Store"}, "FileStore") {
		t.Fatal("ReturnsType must not match a different type name")
	}
}
