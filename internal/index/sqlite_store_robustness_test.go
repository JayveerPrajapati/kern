//go:build !nosqlite

package index

import (
	"os"
	"strings"
	"testing"
)

// TestSQLiteLookupSymbolDeterministic: when several symbols share a name the
// lookup must be deterministic (exact name match first, then the shortest
// qualified name) instead of an arbitrary LIMIT-1 row, and unique names must
// behave exactly like a plain name point-query.
func TestSQLiteLookupSymbolDeterministic(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	dir := t.TempDir()
	ix := New(dir)
	ix.Symbols = []Symbol{
		{Kind: "func", Name: "build", File: "a.go", Line: 1, Lang: "go"},
		{Kind: "method", Name: "build", Receiver: "Foo", File: "b.go", Line: 2, Lang: "go"},
		{Kind: "method", Name: "build", Receiver: "Bar", File: "c.go", Line: 3, Lang: "go"},
		{Kind: "func", Name: "helper", File: "d.go", Line: 4, Lang: "go"},
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatal(err)
	}
	store, err := OpenSQLite(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()

	// Ambiguous "build": the unqualified exact-name symbol wins (shortest
	// qualified name), not an arbitrary rowid-order row.
	sym, err := store.LookupSymbol("build")
	if err != nil {
		t.Fatal(err)
	}
	if sym == nil || sym.Name != "build" || sym.Receiver != "" {
		t.Fatalf("ambiguous name: got %+v, want unqualified build (shortest qualified name)", sym)
	}

	// Receiver-qualified lookup reaches the matching method only.
	sym, err = store.LookupSymbol("Foo.build")
	if err != nil {
		t.Fatal(err)
	}
	if sym == nil || sym.Receiver != "Foo" || sym.Name != "build" {
		t.Fatalf("qualified lookup: got %+v, want Foo.build", sym)
	}
	sym, err = store.LookupSymbol("Bar.build")
	if err != nil {
		t.Fatal(err)
	}
	if sym == nil || sym.Receiver != "Bar" {
		t.Fatalf("qualified lookup: got %+v, want Bar.build", sym)
	}

	// Unique name: identical to a plain point-query result.
	sym, err = store.LookupSymbol("helper")
	if err != nil {
		t.Fatal(err)
	}
	if sym == nil || sym.Name != "helper" {
		t.Fatalf("unique name: got %+v, want helper", sym)
	}

	// Missing name stays a clean nil, nil.
	sym, err = store.LookupSymbol("nope")
	if err != nil {
		t.Fatal(err)
	}
	if sym != nil {
		t.Fatalf("missing name: got %+v, want nil", sym)
	}
}

// TestSQLiteCacheSizeSingleApplySite: cache_size must have exactly one value
// with one authoritative apply site — the DSN must carry the formatted
// sqliteCacheSize _pragma (applied by the driver at connection open), and the
// OpenSQLite Exec pragma list must NOT carry a second, conflicting cache_size
// (oracle-gate: cache_size moved out of the Exec list into the DSN so the
// single-conn pool cannot skip or reorder it). The applied value must equal
// the shared sqliteCacheSize constant.
func TestSQLiteCacheSizeSingleApplySite(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	src, err := os.ReadFile("sqlite_store.go")
	if err != nil {
		t.Fatal(err)
	}
	dsnHasCache := false
	for _, line := range strings.Split(string(src), "\n") {
		if strings.Contains(line, "PRAGMA cache_size") {
			t.Errorf("OpenSQLite Exec pragma list still carries cache_size; the DSN _pragma must be the single apply site: %q", strings.TrimSpace(line))
		}
		if strings.Contains(line, "_pragma=cache_size") {
			dsnHasCache = true
		}
	}
	if !dsnHasCache {
		t.Error("DSN must format sqliteCacheSize into _pragma=cache_size(...); no DSN apply site found")
	}
	store, err := OpenSQLite(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = store.Close() }()
	var size int
	if err := store.db.QueryRow("PRAGMA cache_size").Scan(&size); err != nil {
		t.Fatal(err)
	}
	if size != sqliteCacheSize {
		t.Errorf("PRAGMA cache_size = %d; want %d (sqliteCacheSize)", size, sqliteCacheSize)
	}
}
