package app

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
	"github.com/JayveerPrajapati/kern/internal/intel"
)

// bareMethodFixture writes a tiny module with the same method name on two
// receivers ("dispatch" on Server and on Queue): the bare name is ambiguous
// in the graph (two nodes named dispatch), but intel.Resolve — the resolver
// kern explore uses — maps it to "Server.dispatch" (asrv sorts before zqueue,
// so Server.dispatch is the first index match for the bare name).
func bareMethodFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	files := map[string]string{
		"go.mod":          "module baremethod\n\ngo 1.20\n",
		"asrv/server.go":  "package asrv\n\ntype Server struct{}\n\nfunc (s *Server) dispatch() int { return 1 }\n",
		"zqueue/queue.go": "package zqueue\n\ntype Queue struct{}\n\nfunc (q *Queue) dispatch() int { return 2 }\n",
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

// TestResolveSymbolBareMethodName is the F2 regression: `kern impact dispatch`
// used to error "no symbol named \"dispatch\" was found" even though
// `kern explore dispatch` resolves it to Server.dispatch — resolveSymbol only
// tried the graph resolver and ranked search, never intel.Resolve (the
// resolver explore/context use, which handles bare method names).
func TestResolveSymbolBareMethodName(t *testing.T) {
	if testing.Short() {
		t.Skip("indexes fixture; skipped with -short")
	}
	root := bareMethodFixture(t)
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	p, err := NewWithIndex(root, ix)
	if err != nil {
		t.Fatalf("NewWithIndex: %v", err)
	}
	// Precondition of the defect: explore resolves the bare method name.
	if _, err := intel.Explore(ix, "dispatch", 1, 10); err != nil {
		t.Fatalf("explore must resolve the bare method name: %v", err)
	}
	sym, fuzzy, err := p.resolveSymbol("dispatch")
	if err != nil {
		t.Fatalf("resolveSymbol(dispatch): %v", err)
	}
	if sym != "Server.dispatch" && sym != "asrv.Server.dispatch" {
		t.Errorf("resolveSymbol(dispatch) = %q; want Server.dispatch (the name explore resolves)", sym)
	}
	if !fuzzy {
		t.Error("resolveSymbol(dispatch) must report fuzzy=true so the dispatch -> Server.dispatch substitution is surfaced, not silent")
	}
}
