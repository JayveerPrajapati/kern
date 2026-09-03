//go:build sqlite

package storage

import (
	"database/sql"
	"fmt"

	_ "modernc.org/sqlite"
)

// SQLitePragmas holds the hard-coded database pragmas required for WAL performance
// and multi-agent lock-free concurrency.
var SQLitePragmas = []string{
	"PRAGMA journal_mode=WAL;",
	"PRAGMA synchronous=NORMAL;",
	"PRAGMA busy_timeout=5000;",
	"PRAGMA temp_store=MEMORY;",
	"PRAGMA cache_size=-20000;", // Allocate ~20MB buffer
}

// OpenSQLite opens a SQLite database at path with optimized WAL and concurrency pragmas applied.
func OpenSQLite(path string) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-20000)", path)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("open sqlite: %w", err)
	}

	for _, pragma := range SQLitePragmas {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("exec pragma %s: %w", pragma, err)
		}
	}

	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return db, nil
}
