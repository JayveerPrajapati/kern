package intel

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/tokenize"
)

func TestGraphCtxCallerFirstBudgeted(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)

	out, err := GraphCtx(ix, "Greet", 200)
	if err != nil {
		t.Fatalf("GraphCtx: %v", err)
	}
	// caller-first: the callers section precedes callees.
	ci := strings.Index(out, "callers")
	ce := strings.Index(out, "callees")
	if ci < 0 {
		t.Fatalf("expected callers section, got %q", out)
	}
	if ce >= 0 && ce < ci {
		t.Fatalf("callers must come before callees, got %q", out)
	}
	// names only: no source text from the definition body.
	if strings.Contains(out, "say hello") {
		t.Fatalf("expected names-only output, got source text: %q", out)
	}
	// confidence tags present on edges.
	if !strings.Contains(out, "[EXTRACTED]") {
		t.Fatalf("expected confidence tags, got %q", out)
	}
	// community membership present.
	if !strings.Contains(out, "community") {
		t.Fatalf("expected community membership, got %q", out)
	}
	// budget respected.
	if tok := tokenize.Count(out); tok > 200 {
		t.Fatalf("output %d tokens exceeds budget 200", tok)
	}
}

func TestGraphCtxNoBudgetKeepsAll(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)

	out, err := GraphCtx(ix, "Greet", 0)
	if err != nil {
		t.Fatalf("GraphCtx: %v", err)
	}
	if !strings.Contains(out, "main [EXTRACTED]") {
		t.Fatalf("expected caller main with tag, got %q", out)
	}
}

func TestGraphCtxUnknownSymbol(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)

	if _, err := GraphCtx(ix, "Nope", 200); err == nil || !strings.Contains(err.Error(), "unknown symbol") {
		t.Fatalf("expected unknown-symbol error, got %v", err)
	}
}

func TestGraphCtxEmptySymbol(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)

	if _, err := GraphCtx(ix, "", 200); err == nil || !strings.Contains(err.Error(), "symbol is required") {
		t.Fatalf("expected symbol-required error, got %v", err)
	}
}

func TestGraphCtxUsesPersistedLabels(t *testing.T) {
	root := buildTestProject(t)
	ix := buildIndex(t, root)

	// Simulate a SQLite-loaded index: pre-populated communities skip the
	// on-demand label propagation entirely.
	ix.Communities = map[string]string{"main": "main", "Greet": "main"}

	out, err := GraphCtx(ix, "Greet", 200)
	if err != nil {
		t.Fatalf("GraphCtx: %v", err)
	}
	if !strings.Contains(out, "community (2 members): Greet, main") {
		t.Fatalf("expected persisted-label community, got %q", out)
	}
}

// buildInterfaceProject returns a project with an interface, two concrete
// implementations, a caller that dispatches through the interface, and one
// unrelated type with a same-named method in another package.
func buildInterfaceProject(t *testing.T) string {
	t.Helper()
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := t.TempDir()
	_ = os.WriteFile(filepath.Join(root, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0o644)
	store := "package demo\n\n// Store fetches rows.\ntype Store interface {\n\tFetch() string\n}\n\n// Cat implements Store.\ntype Cat struct{}\n\n// Fetch returns a cat.\nfunc (Cat) Fetch() string { return \"cat\" }\n\n// Dog implements Store.\ntype Dog struct{}\n\n// Fetch returns a dog.\nfunc (Dog) Fetch() string { return \"dog\" }\n"
	_ = os.WriteFile(filepath.Join(root, "store.go"), []byte(store), 0o644)
	user := "package demo\n\n// User loads via the interface.\nfunc User(s Store) string { return s.Fetch() }\n"
	_ = os.WriteFile(filepath.Join(root, "user.go"), []byte(user), 0o644)
	_ = os.MkdirAll(filepath.Join(root, "other"), 0o755)
	other := "package other\n\n// Fetch is unrelated: same method name, different package.\ntype Bird struct{}\n\n// Fetch returns a bird.\nfunc (Bird) Fetch() string { return \"bird\" }\n"
	_ = os.WriteFile(filepath.Join(root, "other", "other.go"), []byte(other), 0o644)
	return root
}

func TestGraphCtxInterfaceDispatchHints(t *testing.T) {
	root := buildInterfaceProject(t)
	ix := buildIndex(t, root)

	out, err := GraphCtx(ix, "User", 400)
	if err != nil {
		t.Fatalf("GraphCtx: %v", err)
	}
	// The interface-method callee is tagged INFERRED with no definition
	// location (the receiver has no symbol of its own).
	if !strings.Contains(out, "Store.Fetch [INFERRED]") {
		t.Fatalf("expected INFERRED interface callee, got %q", out)
	}
	if strings.Contains(out, "Store.Fetch [INFERRED] —") {
		t.Fatalf("interface callee must not carry a fallback location, got %q", out)
	}
	// Dispatch hints list the concrete implementations, sorted.
	if !strings.Contains(out, "    dispatch (INFERRED): Cat.Fetch, Dog.Fetch") {
		t.Fatalf("expected dispatch hints, got %q", out)
	}
	// Same-named method in another package is not a dispatch target.
	if strings.Contains(out, "Bird.Fetch") {
		t.Fatalf("cross-package same-named method must not be a dispatch target, got %q", out)
	}
}

func TestGraphCtxInterfaceMethodRoot(t *testing.T) {
	root := buildInterfaceProject(t)
	ix := buildIndex(t, root)

	out, err := GraphCtx(ix, "Store.Fetch", 400)
	if err != nil {
		t.Fatalf("GraphCtx: %v", err)
	}
	if !strings.Contains(out, "graph Store.Fetch (interface method)") {
		t.Fatalf("expected interface-method header, got %q", out)
	}
	if !strings.Contains(out, "  dispatch (INFERRED): Cat.Fetch, Dog.Fetch") {
		t.Fatalf("expected dispatch hints, got %q", out)
	}
	if !strings.Contains(out, "callers (1):") || !strings.Contains(out, "User [INFERRED]") {
		t.Fatalf("expected the call site as caller, got %q", out)
	}
}

func TestGraphCtxConcreteMethodHasNoDispatch(t *testing.T) {
	root := buildInterfaceProject(t)
	ix := buildIndex(t, root)

	out, err := GraphCtx(ix, "Cat.Fetch", 400)
	if err != nil {
		t.Fatalf("GraphCtx: %v", err)
	}
	if strings.Contains(out, "dispatch (INFERRED)") {
		t.Fatalf("concrete method must not carry dispatch hints, got %q", out)
	}
}
