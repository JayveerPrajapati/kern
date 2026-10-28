//go:build !nosqlite

package index

import (
	"github.com/JayveerPrajapati/kern/internal/indexdb"
	"os"
	"path/filepath"
	"testing"
)

func TestSQLiteJavaCompilerBindingsRoundTrip(t *testing.T) {
	requireJavaCompiler(t)
	root := writeTree(t, map[string]string{"Api.java": "interface Job { int run(int value); } class Good implements Job { public int run(int value) { return value; } } class Api { void call(Job job) { job.run(1); } }"})
	ix, err := BuildWithOptions(root, WithJavaCompiler(""))
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(ix); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	if !loaded.JavaCompiler || loaded.JavaClasspath != ix.JavaClasspath {
		t.Fatal("SQLite lost Java compiler mode")
	}
	before, after := ix.Calls["Api.call"], loaded.Calls["Api.call"]
	if len(before) != 2 || len(after) != 2 {
		t.Fatalf("SQLite lost runtime candidates: before=%+v after=%+v", before, after)
	}
	for position := range before {
		if before[position] != after[position] {
			t.Fatalf("SQLite changed compiler metadata: before=%+v after=%+v", before, after)
		}
	}
	edges, err := store.LookupCalls("Api.call")
	if err != nil || len(edges) != 2 || edges[1] != before[1] {
		t.Fatalf("point query lost Java compiler metadata: %+v, %v", edges, err)
	}
	for _, edge := range loaded.CallBindings().Edges {
		if edge.Binding != "javac" || edge.Resolution != "resolved" {
			t.Fatalf("loaded compiler edge lost provenance: %+v", edge)
		}
	}
}

func TestSQLiteGoCompilerBindingsRoundTrip(t *testing.T) {
	root := writeTree(t, map[string]string{
		"go.mod":  "module demo\n\ngo 1.22\n",
		"main.go": "package demo\nfunc Use() { Work() }\nfunc Work() {}\n",
	})
	ix, err := BuildWithOptions(root, WithGoTypes())
	if err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(root)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if err := store.Save(ix); err != nil {
		t.Fatal(err)
	}
	loaded, err := store.Load()
	if err != nil {
		t.Fatal(err)
	}
	want := ix.Calls["Use"][0]
	if want.ToID == "" || !loaded.GoTypes || len(loaded.Calls["Use"]) != 1 || loaded.Calls["Use"][0] != want {
		t.Fatalf("SQLite dropped compiler metadata: %+v", loaded.Calls["Use"])
	}
	edges, err := store.LookupCalls("Use")
	if err != nil || len(edges) != 1 || edges[0] != want {
		t.Fatalf("point query dropped compiler metadata: %+v, %v", edges, err)
	}
	graph := loaded.CallBindings()
	if len(graph.Edges) != 1 || graph.Edges[0].From != want.FromID || graph.Edges[0].To != want.ToID || graph.Edges[0].Resolution != "resolved" {
		t.Fatalf("loaded graph lost compiler identity: %+v", graph)
	}
}

func TestSQLiteRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := writeTree(t, map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSQLite(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got == nil {
		t.Fatal("nil index after load")
	}
	if len(got.Symbols) != len(ix.Symbols) {
		t.Errorf("symbol count = %d; want %d", len(got.Symbols), len(ix.Symbols))
	}
	if len(got.Calls) != len(ix.Calls) {
		t.Errorf("caller-map count = %d; want %d", len(got.Calls), len(ix.Calls))
	}
	for k, v := range ix.Calls {
		if !equalCallEdges(got.Calls[k], v) {
			t.Errorf("Calls[%q] = %v; want %v", k, got.Calls[k], v)
		}
	}
	for k, v := range ix.Callers {
		if !equalStrings(got.Callers[k], v) {
			t.Errorf("Callers[%q] = %v; want %v", k, got.Callers[k], v)
		}
	}
	for p, want := range ix.Pkgs {
		gotPkg := got.Pkgs[p]
		if gotPkg == nil {
			t.Errorf("missing package %s", p)
			continue
		}
		if gotPkg.Name != want.Name {
			t.Errorf("pkg %s name = %q; want %q", p, gotPkg.Name, want.Name)
		}
	}
	for f, want := range ix.ImportsByFile {
		if !equalImportEdges(got.ImportsByFile[f], want) {
			t.Errorf("ImportsByFile[%q] = %v; want %v", f, got.ImportsByFile[f], want)
		}
	}
	for f, h := range ix.FileHashes {
		if got.FileHashes[f] != h {
			t.Errorf("file hash %s mismatch", f)
		}
	}
	if len(got.Communities) == 0 {
		t.Errorf("communities not persisted: loaded %d labels", len(got.Communities))
	}
	wantLabels := ix.CommunityLabels()
	for sym, want := range wantLabels {
		if got.Communities[sym] != want {
			t.Errorf("community of %q = %q; want %q", sym, got.Communities[sym], want)
		}
	}
	if got.MaxMtime != ix.MaxMtime {
		t.Errorf("MaxMtime = %d; want %d", got.MaxMtime, ix.MaxMtime)
	}
}

func TestSQLiteCallsKindColumn(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := writeTree(t, map[string]string{"main.go": srcMain, "user.go": srcOther})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	if !indexdb.HasColumn(store.db, "calls", "kind") {
		t.Fatal("calls table missing kind column")
	}
	if !indexdb.HasColumn(store.db, "calls", "source_file") {
		t.Fatal("calls table missing source_file column")
	}
	if !indexdb.HasColumn(store.db, "communities", "symbol") {
		t.Fatal("communities table missing symbol column")
	}
	var kinds int
	if err := store.db.QueryRow("SELECT COUNT(*) FROM calls WHERE kind='call'").Scan(&kinds); err != nil {
		t.Fatal(err)
	}
	if kinds == 0 {
		t.Fatal("expected call edges with kind='call'")
	}
}

func TestSQLiteSourceIdentityRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := writeTree(t, map[string]string{
		"a/source.go": "package a\nimport \"strings\"\nfunc Shared() { aHelper(); strings.TrimSpace(\"a\") }\nfunc aHelper() {}\n",
		"b/source.go": "package b\nimport \"strings\"\nfunc Shared() { bHelper() }\nfunc bHelper() { strings.TrimSpace(\"b\") }\n",
	})
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSQLite(root, ix); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSQLite(root)
	if err != nil {
		t.Fatal(err)
	}
	assertSourceCalls(t, loaded, "go")
	if !equalCallEdges(loaded.Calls["Shared"], ix.Calls["Shared"]) {
		t.Errorf("loaded shared edges = %v, want %v", loaded.Calls["Shared"], ix.Calls["Shared"])
	}
}

func TestSQLiteAliasAndDynamicRoundTrip(t *testing.T) {
	ix := aliasPersistenceFixture(t)
	if err := SaveSQLite(ix.Root, ix); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSQLite(ix.Root)
	if err != nil {
		t.Fatal(err)
	}
	assertBindingPersistence(t, ix, loaded)
	store, err := OpenSQLite(ix.Root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	edges, err := store.LookupCalls("Use")
	if err != nil {
		t.Fatal(err)
	}
	if !equalCallEdges(edges, ix.Calls["Use"]) {
		t.Fatalf("point lookup lost dynamic/source binding: %+v", edges)
	}
}

func TestSQLiteDeclarationIdentityRoundTrip(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := writeTree(t, map[string]string{"source.go": "package fixture\nfunc Shared() {}\n"})
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	ix.Symbols[0].Outer = "Container"
	ix.Symbols[0].Params = []string{"int", "string"}
	want := ix.Symbols[0].DeclarationID()
	if err := SaveSQLite(root, ix); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadSQLite(root)
	if err != nil {
		t.Fatal(err)
	}
	if got := loaded.Symbols[0].DeclarationID(); got != want {
		t.Errorf("loaded declaration identity = %s, want %s", got, want)
	}
	store, err := OpenSQLite(root)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	symbol, err := store.LookupSymbol("Shared")
	if err != nil || symbol == nil {
		t.Fatalf("LookupSymbol = %v, error %v", symbol, err)
	}
	if symbol.DeclarationID() != want {
		t.Errorf("point lookup lost declaration identity: %+v", symbol)
	}
	fileSymbols, err := store.LookupFileSymbols("source.go")
	if err != nil {
		t.Fatal(err)
	}
	if len(fileSymbols) != 1 || fileSymbols[0].DeclarationID() != want {
		t.Errorf("file lookup lost declaration identity: %+v", fileSymbols)
	}
	searchSymbols, err := store.SearchFTS("Shared", 5)
	if err != nil {
		t.Fatal(err)
	}
	if len(searchSymbols) != 1 || searchSymbols[0].DeclarationID() != want {
		t.Errorf("FTS lookup lost declaration identity: %+v", searchSymbols)
	}
}

func TestSQLiteStaleAfterEdit(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatal(err)
	}
	got, err := LoadSQLite(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got.Stale() {
		t.Fatal("index should be fresh")
	}
	// Edit a file; the SQLite-loaded index must detect the change.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte(srcMain+`
func extra() {}
`), 0o644); err != nil {
		t.Fatal(err)
	}
	got2, err := LoadSQLite(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !got2.Stale() {
		t.Fatal("index should be stale after edit")
	}
}

func TestSQLiteSearchFTS(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatal(err)
	}
	got, err := FTS5Search(dir, "greet", 10)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) == 0 {
		t.Fatal("fts search for greet returned nothing")
	}
	found := false
	for _, s := range got {
		if s.Name == "greet" {
			found = true
		}
	}
	if !found {
		t.Errorf("fts search did not return greet; got %+v", got)
	}
}

func TestSQLiteEmptyStoreSearch(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	_, err := FTS5Search(dir, "greet", 10)
	if err == nil {
		t.Fatal("expected error searching a store with no index")
	}
}

func TestFTS5HostileQueries(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := writeTree(t, map[string]string{"main.go": srcMain})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatal(err)
	}
	// None of these may produce an FTS5 syntax error; punctuation must be
	// escaped and stray operators neutralised, not passed through raw.
	for _, q := range []string{"greet AND", "greet(bar)", `greet"x`, "AND greet OR", "NOT", `(greet`, "greet OR bye OR"} {
		if _, err := FTS5Search(dir, q, 10); err != nil {
			t.Errorf("FTS5Search(%q) failed: %v", q, err)
		}
	}
	// The documented column-filter and operator syntax must keep working.
	for _, q := range []string{`file:"main.go"`, "greet OR nonexistent"} {
		got, err := FTS5Search(dir, q, 10)
		if err != nil {
			t.Fatalf("FTS5Search(%q) failed: %v", q, err)
		}
		if len(got) == 0 {
			t.Errorf("FTS5Search(%q) returned nothing", q)
		}
	}
}

func equalCallEdges(a, b []CallEdge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalImportEdges(a, b []ImportEdge) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestSQLitePointQueries(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := writeTree(t, map[string]string{
		"main.go": srcMain,
		"user.go": srcOther,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatal(err)
	}

	store, err := OpenSQLite(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// 1. LookupSymbol
	sym, err := store.LookupSymbol("greet")
	if err != nil {
		t.Fatalf("LookupSymbol(greet) err: %v", err)
	}
	if sym == nil || sym.Name != "greet" {
		t.Fatalf("LookupSymbol(greet) got %+v, want name 'greet'", sym)
	}

	symNone, err := store.LookupSymbol("nonexistent")
	if err != nil {
		t.Fatalf("LookupSymbol(nonexistent) err: %v", err)
	}
	if symNone != nil {
		t.Fatalf("LookupSymbol(nonexistent) expected nil, got %+v", symNone)
	}

	// 2. LookupCallers
	callers, err := store.LookupCallers("greet")
	if err != nil {
		t.Fatalf("LookupCallers(greet) err: %v", err)
	}
	if len(callers) == 0 {
		t.Fatalf("LookupCallers(greet) got 0 callers, expected main or User.Login")
	}

	// 3. LookupCalls
	calls, err := store.LookupCalls("main")
	if err != nil {
		t.Fatalf("LookupCalls(main) err: %v", err)
	}
	if len(calls) == 0 {
		t.Fatalf("LookupCalls(main) got 0 calls, expected call to greet")
	}
	for _, edge := range calls {
		if edge.SourceFile != "main.go" {
			t.Errorf("point lookup lost call source: %+v", edge)
		}
	}

	// 4. LookupFileSymbols
	fileSyms, err := store.LookupFileSymbols("main.go")
	if err != nil {
		t.Fatalf("LookupFileSymbols(main.go) err: %v", err)
	}
	if len(fileSyms) < 2 {
		t.Fatalf("LookupFileSymbols(main.go) got %d symbols, expected at least main and greet", len(fileSyms))
	}

	// 5. LookupInherits / LookupInheritedBy (empty for plain Go code without embedded/subtypes)
	inherits, err := store.LookupInherits("User")
	if err != nil {
		t.Fatalf("LookupInherits(User) err: %v", err)
	}
	_ = inherits
}
