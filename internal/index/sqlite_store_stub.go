//go:build nosqlite

package index

import (
	"errors"
	"time"
)

// SQLiteEnabled reports whether the SQLite persistent store is available in
// this build. SQLite is DEFAULT-ON: the real store (sqlite_store.go) is
// compiled in unless the build is tagged -tags nosqlite, which selects this
// stub. The stub returns false so callers degrade gracefully to the JSON
// cache.
func SQLiteEnabled() bool { return false }

// SQLitePath is unavailable in the nosqlite stub build.
func SQLitePath(root string) string { return "" }

// SQLiteStore is the type used by the sqlite build; the stub build never
// constructs one.
type SQLiteStore struct{}

// Close is a no-op in the stub build.
func (s *SQLiteStore) Close() error { return nil }

func (s *SQLiteStore) SetMeta(key, value string) error { return errSQLiteNotEnabled }

// LookupSymbol is unavailable in the nosqlite stub build.
func (s *SQLiteStore) LookupSymbol(name string) (*Symbol, error) { return nil, errSQLiteNotEnabled }

// LookupCallers is unavailable in the nosqlite stub build.
func (s *SQLiteStore) LookupCallers(callee string) ([]string, error) { return nil, errSQLiteNotEnabled }

// LookupCalls is unavailable in the nosqlite stub build.
func (s *SQLiteStore) LookupCalls(caller string) ([]CallEdge, error) { return nil, errSQLiteNotEnabled }

// LookupFileSymbols is unavailable in the nosqlite stub build.
func (s *SQLiteStore) LookupFileSymbols(file string) ([]Symbol, error) {
	return nil, errSQLiteNotEnabled
}

// LookupInherits is unavailable in the nosqlite stub build.
func (s *SQLiteStore) LookupInherits(subtype string) ([]string, error) {
	return nil, errSQLiteNotEnabled
}

// OpenSQLite is unavailable in the nosqlite stub build.
func OpenSQLite(root string) (*SQLiteStore, error) {
	return nil, errSQLiteNotEnabled
}

// SaveSQLiteDelta is unavailable in the nosqlite stub build: the JSON cache is
// the only format there, and every save is a full write.
func SaveSQLiteDelta(root string, ix *Index, plan *deltaPlan) error { return errSQLiteNotEnabled }

// SaveSQLite is unavailable in the nosqlite stub build.
func SaveSQLite(root string, ix *Index) error { return errSQLiteNotEnabled }

// LoadSQLite is unavailable in the nosqlite stub build.
func LoadSQLite(root string) (*Index, error) { return nil, errSQLiteNotEnabled }

// FTS5Search is unavailable in the nosqlite stub build.
func FTS5Search(root, query string, limit int) ([]Symbol, error) {
	return nil, errSQLiteNotEnabled
}

// SQLiteSnapshotValid is unavailable in the nosqlite stub build: there is no
// SQLite store, so a snapshot can never be validated (Load rebuilds).
func SQLiteSnapshotValid(root, token string, snapshotUpdated time.Time) bool { return false }

// LoadMetadata is unavailable in the nosqlite stub build; callers fall back
// to the full JSON load.
func LoadMetadata(root string) (*Metadata, error) { return nil, errSQLiteNotEnabled }

// errSQLiteNotEnabled is returned when a SQLite feature is used in a build
// compiled with -tags nosqlite (the only build in which the store is absent).
var errSQLiteNotEnabled = errors.New("sqlite store not enabled (compiled with -tags nosqlite)")
