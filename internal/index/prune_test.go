package index

import (
	"os"
	"testing"
)

// TestPruneLegacyJSON pins the P2-3 retention migration: once a SQLite store
// exists, a leftover frozen index.json is removed by `kern index`, while the
// derived fast snapshot (the live SQLite load path) is kept.
func TestPruneLegacyJSON(t *testing.T) {
	if !SQLiteEnabled() {
		t.Skip("sqlite not compiled in")
	}
	dir := t.TempDir()
	ix := fixtureIndex(dir)
	if err := ix.Save(); err != nil {
		t.Fatalf("Save: %v", err)
	}
	jsonPath := StorePath(dir)
	if err := os.WriteFile(jsonPath, []byte("{\"legacy\":true}"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := LegacyJSONSize(dir); got == 0 {
		t.Fatal("LegacyJSONSize = 0 with both stores present")
	}
	if !PruneLegacyJSON(dir) {
		t.Fatal("PruneLegacyJSON returned false with a legacy JSON + SQLite store present")
	}
	if _, err := os.Stat(jsonPath); !os.IsNotExist(err) {
		t.Fatalf("legacy index.json still present, stat err = %v", err)
	}
	if _, err := os.Stat(binSnapshotPathFor(jsonPath)); err != nil {
		t.Fatalf("derived snapshot must be kept: %v", err)
	}
	if PruneLegacyJSON(dir) {
		t.Fatal("second PruneLegacyJSON reported a removal with no JSON present")
	}
}
