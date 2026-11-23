package indexdb

import (
	"database/sql"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"
)

// openTestDB applies the schema to a throwaway store. The driver is
// registered here via a test-only import (indexdb deliberately has no driver
// dependency; internal/index registers it in production binaries).
func openTestDB(t *testing.T) *sql.DB {
	t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open sqlite: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := ApplySchema(db); err != nil {
		t.Fatalf("apply schema: %v", err)
	}
	return db
}

// TestMaybeCompactFTSChurnCounter pins the delta-path compaction budget
// (F-C4, 2026-11-22 dogfood): churn accumulates in meta until the threshold,
// then resets — the compaction trigger is deterministic, not size-heuristic.
func TestMaybeCompactFTSChurnCounter(t *testing.T) {
	db := openTestDB(t)
	churn := func() string {
		var v string
		if err := db.QueryRow(`SELECT value FROM meta WHERE key='fts_churn'`).Scan(&v); err != nil {
			return ""
		}
		return v
	}
	// Seed one fts row so optimize has content to work on.
	if _, err := db.Exec(`INSERT INTO symbols_fts(rowid, kind, name, receiver, file, params) VALUES(1,'func','seeder','r','f.go','[]')`); err != nil {
		t.Fatalf("seed fts: %v", err)
	}

	MaybeCompactFTS(db, "unused", 0) // no-op, must not touch the counter
	if got := churn(); got != "" {
		t.Fatalf("touched=0 must leave the counter unset, got %q", got)
	}
	MaybeCompactFTS(db, "unused", 100)
	if got := churn(); got != "100" {
		t.Fatalf("churn after 100 = %q, want 100", got)
	}
	MaybeCompactFTS(db, "unused", 150)
	if got := churn(); got != "250" {
		t.Fatalf("churn after 100+150 = %q, want 250 (still below threshold)", got)
	}
	// Crossing the threshold compacts and resets the budget.
	MaybeCompactFTS(db, "unused", 10)
	if got := churn(); got != "0" {
		t.Fatalf("churn after crossing the threshold = %q, want 0 (reset after compaction)", got)
	}
}
