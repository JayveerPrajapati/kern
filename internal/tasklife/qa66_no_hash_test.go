package tasklife

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TestImpactFuzzyResolutionNoDeclHash (QA 6.6): a change whose bare name is
// ambiguous ("dispatch" in two packages) resolves through a decl:<sha>
// identity (tasklife.resolveSymbol maps receiver/name-qualified bare names
// through DeclarationID). The rendered impact payload — the fuzzy banner AND
// the report header — must name the picked definition, never the machine
// hash. The structured ImpactReport.Target intentionally keeps the raw ID for
// --json / machine consumers; only the rendered text is display-mapped.
func TestImpactFuzzyResolutionNoDeclHash(t *testing.T) {
	root := t.TempDir()
	files := map[string]string{
		"go.mod": "module amb\n\ngo 1.27\n",
		"a/a.go": "package a\n\n// dispatch routes a request.\nfunc dispatch() {}\n",
		"b/b.go": "package b\n\n// dispatch routes the other request.\nfunc dispatch() {}\n",
	}
	for rel, content := range files {
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	ix, err := index.Build(root)
	if err != nil {
		t.Fatalf("index.Build: %v", err)
	}
	p := newTestPlatformWithIndex(t, root, ix)
	ts := NewTaskService(p, nil)

	_, _, text, err := ts.Impact("dispatch")
	if err != nil {
		t.Fatalf("Impact(dispatch): %v", err)
	}
	if strings.Contains(text, "decl:") {
		t.Errorf("impact text must not carry decl: hashes (QA 6.6):\n%s", text)
	}
	// The ambiguous change must surface the fuzzy banner naming a real
	// definition (a.dispatch or b.dispatch), and the header must carry the
	// same name — never the raw identity.
	if !strings.Contains(text, "fuzzy match") {
		t.Errorf("ambiguous change must surface the fuzzy banner:\n%s", text)
	}
	if !strings.Contains(text, "IMPACT for: ") || !strings.Contains(text, ".dispatch") {
		t.Errorf("impact text must name the resolved definition (pkg.dispatch), got:\n%s", text)
	}
	// The banner must name a concrete definition, not the query word itself.
	if !strings.Contains(text, `resolved "dispatch" -> `) {
		t.Errorf("fuzzy banner must show the query -> definition mapping:\n%s", text)
	}
}
