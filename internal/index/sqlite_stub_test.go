//go:build nosqlite

package index

import "testing"

func TestPersistIdentityLabelWithoutSQLite(t *testing.T) {
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	root := writeTree(t, map[string]string{"source.go": "package fixture\nfunc Shared() {}\n"})
	ix, err := Build(root)
	if err != nil {
		t.Fatal(err)
	}
	ix.Identity.GitCommit = "updated-label"
	if err := ix.persistIdentityLabel(); err != nil {
		t.Fatal(err)
	}
	loaded, err := Load(root)
	if err != nil {
		t.Fatal(err)
	}
	if loaded.Identity == nil || loaded.Identity.GitCommit != "updated-label" {
		t.Errorf("JSON fallback lost identity label: %+v", loaded.Identity)
	}
	store := &SQLiteStore{}
	if err := store.SetMeta("identity", "{}"); err != errSQLiteNotEnabled {
		t.Errorf("stub SetMeta = %v, want unavailable error", err)
	}
}
