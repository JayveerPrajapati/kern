package root

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/indexdb"
)

// TestPlausibleProjectSQLiteOnly pins the SQLite recognition contract: a
// non-git project with a populated .kern/index.sqlite is a project, while a
// bare/empty/schema-only store is not (the server's own state init can create
// one in a scratch cwd — counting it would reintroduce the M#18 bootstrap).
func TestPlausibleProjectSQLiteOnly(t *testing.T) {
	mk := func(t *testing.T, populate bool) string {
		t.Helper()
		dir := t.TempDir()
		store := filepath.Join(dir, ".kern")
		if err := os.MkdirAll(store, 0o755); err != nil {
			t.Fatal(err)
		}
		db, err := indexdb.OpenReadWrite(filepath.Join(store, "index.sqlite"), 1024)
		if err != nil {
			t.Skipf("sqlite not available: %v", err)
		}
		if err := indexdb.ApplySchema(db); err != nil {
			_ = db.Close()
			t.Fatal(err)
		}
		if populate {
			if _, err := db.Exec(`INSERT INTO symbols(kind,name,file,line) VALUES('func','A','a.go',1)`); err != nil {
				_ = db.Close()
				t.Fatal(err)
			}
		}
		if err := db.Close(); err != nil {
			t.Fatal(err)
		}
		return dir
	}

	if !PlausibleProject(mk(t, true)) {
		t.Fatal("a non-git project with a populated .kern/index.sqlite must be plausible")
	}
	if PlausibleProject(mk(t, false)) {
		t.Fatal("a schema-only .kern/index.sqlite must not make a scratch dir a project")
	}

	empty := t.TempDir()
	if err := os.MkdirAll(filepath.Join(empty, ".kern"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(empty, ".kern", "index.sqlite"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
	if PlausibleProject(empty) {
		t.Fatal("a zero-byte .kern/index.sqlite must not make a scratch dir a project")
	}
}
