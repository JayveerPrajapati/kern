//go:build !sqlite

package storage

import (
	"database/sql"
	"fmt"
)

// OpenSQLite is a stub returning an error when compiled without -tags sqlite.
func OpenSQLite(path string) (*sql.DB, error) {
	return nil, fmt.Errorf("sqlite backend not compiled in (build with -tags sqlite)")
}
