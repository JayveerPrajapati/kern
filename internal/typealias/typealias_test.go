package typealias

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// aliasFixture writes a three-package module where an alias (internal/alias)
// renames a type declared in internal/base, and a third package references
// both names. It mirrors the audit case (internal/index.Symbol =
// asttypes.Symbol) at unit scale.
func aliasFixture(t *testing.T) *index.Index {
	t.Helper()
	root := t.TempDir()
	files := map[string]string{
		"internal/base/base.go": "package base\n\ntype Thing struct{}\n\nfunc (t Thing) Do() {}\n\nfunc (t Thing) Undo() {}\n",
		"internal/alias/a.go":   "package alias\n\nimport \"m/internal/base\"\n\ntype T = base.Thing\n\ntype Thing = base.Thing\n",
		"internal/use/use.go":   "package use\n\nimport (\n\t\"m/internal/alias\"\n\t\"m/internal/base\"\n)\n\nvar _ alias.T\nvar _ base.Thing\nvar _ alias.Thing\n",
	}
	for rel, src := range files {
		p := filepath.Join(root, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(src), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return &index.Index{
		Root: root,
		Symbols: []index.Symbol{
			{Kind: "struct", Name: "Thing", File: "internal/base/base.go", Line: 3, Lang: "go"},
			{Kind: "method", Name: "Do", Receiver: "Thing", File: "internal/base/base.go", Line: 5, Lang: "go"},
			{Kind: "method", Name: "Undo", Receiver: "Thing", File: "internal/base/base.go", Line: 7, Lang: "go"},
			{Kind: "type", Name: "T", Alias: "base.Thing", File: "internal/alias/a.go", Line: 5, Lang: "go"},
			{Kind: "type", Name: "Thing", Alias: "base.Thing", File: "internal/alias/a.go", Line: 7, Lang: "go"},
		},
		FileHashes: map[string]string{
			"internal/base/base.go": "h1",
			"internal/alias/a.go":   "h2",
			"internal/use/use.go":   "h3",
		},
		Pkgs: map[string]*index.Pkg{
			"internal/base":  {Name: "base", Path: "internal/base", Lang: "go"},
			"internal/alias": {Name: "alias", Path: "internal/alias", Lang: "go"},
			"internal/use":   {Name: "use", Path: "internal/use", Lang: "go"},
		},
		Calls:       map[string][]index.CallEdge{},
		Callers:     map[string][]string{},
		Inherits:    map[string][]string{},
		Communities: map[string]string{},
	}
}

func TestTargetFollowsQualifiedAlias(t *testing.T) {
	ix := aliasFixture(t)
	tgt, ok := Target(ix, &ix.Symbols[3])
	if !ok || tgt == nil || tgt.Name != "Thing" || tgt.File != "internal/base/base.go" {
		t.Fatalf("Target(T) = %+v, %v; want internal/base Thing", tgt, ok)
	}
	// A definition is not an alias.
	if _, ok := Target(ix, &ix.Symbols[0]); ok {
		t.Fatal("Target(struct Thing) reported an alias")
	}
}

func TestAliasesOfIsReverseAndCrossName(t *testing.T) {
	ix := aliasFixture(t)
	got := AliasesOf(ix, &ix.Symbols[0])
	names := map[string]bool{}
	for _, s := range got {
		names[s.File+":"+s.Name] = true
	}
	if !names["internal/alias/a.go:T"] || !names["internal/alias/a.go:Thing"] {
		t.Fatalf("AliasesOf(Thing) = %+v; want both alias declarations", got)
	}
	if len(Related(ix, &ix.Symbols[3])) != 2 {
		t.Fatalf("Related(T) = %+v; want T plus its target", Related(ix, &ix.Symbols[3]))
	}
}

func TestReferencesUnionBothNames(t *testing.T) {
	ix := aliasFixture(t)
	tAlias := &ix.Symbols[3]
	target := &ix.Symbols[0]
	aliasRefs, ok := References(ix, tAlias)
	if !ok || len(aliasRefs) == 0 {
		t.Fatalf("References(T) = %d refs, complete=%v; want the alias's own uses", len(aliasRefs), ok)
	}
	targetRefs, ok := References(ix, target)
	if !ok || len(targetRefs) <= len(aliasRefs) {
		t.Fatalf("References(Thing) = %d refs; want the union including alias-named uses (alias alone: %d)", len(targetRefs), len(aliasRefs))
	}
	files := map[string]bool{}
	for _, r := range targetRefs {
		files[r.File] = true
	}
	if !files["internal/use/use.go"] {
		t.Fatalf("References(Thing) missed the use package: %+v", targetRefs)
	}
}
