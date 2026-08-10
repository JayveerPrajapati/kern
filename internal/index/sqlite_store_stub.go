//go:build !sqlite

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
