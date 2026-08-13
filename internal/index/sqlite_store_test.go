//go:build sqlite

package index

import (
	"os"
	"path/filepath"
	"testing"
)

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
		if !equalStrings(got.Calls[k], v) {
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
	defer store.Close()
	if !storeHasColumn(store.db, "calls", "kind") {
		t.Fatal("calls table missing kind column")
	}
	if !storeHasColumn(store.db, "communities", "symbol") {
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

func equalStrings(a, b []string) bool {
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
