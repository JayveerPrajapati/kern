//go:build nosqlite

package index

import "errors"

// SQLiteEnabled reports whether the SQLite persistent store is available in
// this build. The default build excludes it (zero-dependency); build with
// -tags sqlite to enable WAL/FTS5 storage.
func SQLiteEnabled() bool { return false }

// SQLitePath is unavailable in the default build.
func SQLitePath(root string) string { return "" }

// SQLiteStore is the type used by the sqlite build; the stub build never
// constructs one.
type SQLiteStore struct{}

// Close is a no-op in the stub build.
func (s *SQLiteStore) Close() error { return nil }

// LookupSymbol is unavailable in the default build.
func (s *SQLiteStore) LookupSymbol(name string) (*Symbol, error) { return nil, errSQLiteNotEnabled }

// LookupCallers is unavailable in the default build.
func (s *SQLiteStore) LookupCallers(callee string) ([]string, error) { return nil, errSQLiteNotEnabled }

// LookupCalls is unavailable in the default build.
func (s *SQLiteStore) LookupCalls(caller string) ([]CallEdge, error) { return nil, errSQLiteNotEnabled }

// LookupFileSymbols is unavailable in the default build.
func (s *SQLiteStore) LookupFileSymbols(file string) ([]Symbol, error) { return nil, errSQLiteNotEnabled }

// LookupInherits is unavailable in the default build.
func (s *SQLiteStore) LookupInherits(subtype string) ([]string, error) { return nil, errSQLiteNotEnabled }

// LookupInheritedBy is unavailable in the default build.
func (s *SQLiteStore) LookupInheritedBy(base string) ([]string, error) { return nil, errSQLiteNotEnabled }

// OpenSQLite is unavailable in the default build.
func OpenSQLite(root string) (*SQLiteStore, error) {
	return nil, errSQLiteNotEnabled
}

// SaveSQLite is unavailable in the default build.
func SaveSQLite(root string, ix *Index) error { return errSQLiteNotEnabled }

// LoadSQLite is unavailable in the default build.
func LoadSQLite(root string) (*Index, error) { return nil, errSQLiteNotEnabled }

// FTS5Search is unavailable in the default build.
func FTS5Search(root, query string, limit int) ([]Symbol, error) {
	return nil, errSQLiteNotEnabled
}

// errSQLiteNotEnabled is returned when a SQLite feature is used without the
// sqlite build tag.
var errSQLiteNotEnabled = errors.New("sqlite store not enabled (build with -tags sqlite)")
