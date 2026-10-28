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
