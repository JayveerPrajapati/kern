//go:build sqlite

package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

func TestOpenSQLite_CreatesSchema(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	defer s.Close()

	for _, table := range []string{
		"meta", "symbols", "calls", "callers", "communities",
		"inherits", "packages", "file_imports", "files", "symbols_fts",
	} {
		var n int
		if err := s.db.QueryRow("SELECT COUNT(*) FROM sqlite_master WHERE name=?", table).Scan(&n); err != nil {
			t.Fatalf("query for table %s: %v", table, err)
		}
		if n != 1 {
			t.Errorf("table %s not created by schema", table)
		}
	}
	if !SQLiteEnabled() {
		t.Error("SQLiteEnabled() = false under -tags sqlite")
	}
}

func TestClose_Path(t *testing.T) {
	dir := t.TempDir()
	s, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	want := filepath.Join(dir, ".kern", "index.sqlite")
	if got := s.Path(); got != want {
		t.Errorf("Path() = %q, want %q", got, want)
	}
	if got := SQLitePath(dir); got != want {
		t.Errorf("SQLitePath(%q) = %q, want %q", dir, got, want)
	}
	if err := s.Close(); err != nil {
		t.Errorf("Close: %v", err)
	}
	// The database file must exist on disk after open+close.
	if _, err := os.Stat(want); err != nil {
		t.Errorf("db file %s missing: %v", want, err)
	}
	// Double close must be safe.
	if err := s.Close(); err != nil {
		t.Errorf("second Close: %v", err)
	}
}

func TestSave_ErrorPaths(t *testing.T) {
	t.Run("closed store", func(t *testing.T) {
		dir := t.TempDir()
		s, err := OpenSQLite(dir)
		if err != nil {
			t.Fatalf("OpenSQLite: %v", err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		ix := New(dir)
		ix.Symbols = []Symbol{{Name: "a", Kind: "func", File: "a.go", Line: 1}}
		if err := s.Save(ix); err == nil {
			t.Error("Save on closed store: want error")
		}
	})
	t.Run("corrupt database", func(t *testing.T) {
		dir := t.TempDir()
		if err := os.MkdirAll(filepath.Join(dir, ".kern"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, ".kern", "index.sqlite"), []byte("definitely not a sqlite database"), 0o644); err != nil {
			t.Fatal(err)
		}
		if _, err := OpenSQLite(dir); err == nil {
			t.Error("OpenSQLite on corrupt file: want error")
		}
	})
}

func TestLoad_MissingDB(t *testing.T) {
	dir := t.TempDir()
	ix, err := LoadSQLite(dir)
	// A fresh store (no meta rows) loads as an empty index with no error.
	if err != nil {
		t.Fatalf("LoadSQLite on fresh store: %v", err)
	}
	if ix != nil {
		t.Errorf("expected nil index for missing store, got %d symbols", len(ix.Symbols))
	}
}

func TestSearchFTS_Limit(t *testing.T) {
	files := map[string]string{}
	for i := 0; i < 6; i++ {
		files[fmt.Sprintf("p%d/a.go", i)] = fmt.Sprintf("package p%d\n\nfunc greet() {}\n", i)
	}
	dir := writeTree(t, files)
	ix, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	if err := SaveSQLite(dir, ix); err != nil {
		t.Fatalf("SaveSQLite: %v", err)
	}

	got, err := FTS5Search(dir, "greet", 2)
	if err != nil {
		t.Fatalf("FTS5Search limit=2: %v", err)
	}
	if len(got) != 2 {
		t.Errorf("limit 2 returned %d results, want 2", len(got))
	}
	// limit <= 0 falls back to the default of 20 → all matches returned.
	for _, lim := range []int{0, -1, 100} {
		got, err := FTS5Search(dir, "greet", lim)
		if err != nil {
			t.Fatalf("FTS5Search limit=%d: %v", lim, err)
		}
		if len(got) != 6 {
			t.Errorf("limit %d returned %d results, want 6", lim, len(got))
		}
	}
}

func TestReopenExisting(t *testing.T) {
	dir := writeTree(t, map[string]string{"main.go": srcMain, "user.go": srcOther})
	ix, err := Build(dir)
	if err != nil {
		t.Fatalf("Build: %v", err)
	}
	s1, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("OpenSQLite: %v", err)
	}
	if err := s1.Save(ix); err != nil {
		t.Fatalf("Save: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// Reopen and load — data must persist across close/reopen.
	s2, err := OpenSQLite(dir)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer s2.Close()
	got, err := s2.Load()
	if err != nil {
		t.Fatalf("Load after reopen: %v", err)
	}
	if got == nil {
		t.Fatal("Load returned nil index")
	}
	if len(got.Symbols) != len(ix.Symbols) {
		t.Errorf("symbol count = %d, want %d", len(got.Symbols), len(ix.Symbols))
	}
	for k, v := range ix.Calls {
		if !equalStrings(got.Calls[k], v) {
			t.Errorf("Calls[%q] = %v, want %v", k, got.Calls[k], v)
		}
	}
}
