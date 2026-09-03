//go:build sqlite

package storage

import (
	"fmt"
	"path/filepath"
	"sync"
	"testing"
)

func TestConcurrentSQLiteWALPragmas(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "wal_test.db")

	db, err := OpenSQLite(dbPath)
	if err != nil {
		t.Fatalf("OpenSQLite failed: %v", err)
	}
	defer db.Close()

	// Verify pragmas
	var journalMode string
	if err := db.QueryRow("PRAGMA journal_mode;").Scan(&journalMode); err != nil {
		t.Fatalf("check journal_mode: %v", err)
	}
	if journalMode != "wal" {
		t.Errorf("journal_mode = %s, want wal", journalMode)
	}

	_, err = db.Exec("CREATE TABLE IF NOT EXISTS kv (k TEXT PRIMARY KEY, v TEXT);")
	if err != nil {
		t.Fatalf("create table: %v", err)
	}

	// Concurrent read and write test across multiple agents
	const numWriters = 5
	const numReaders = 10
	var wg sync.WaitGroup

	for w := 0; w < numWriters; w++ {
		wg.Add(1)
		go func(writerID int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				key := fmt.Sprintf("w%d_k%d", writerID, i)
				val := fmt.Sprintf("val_%d", i)
				_, err := db.Exec("INSERT OR REPLACE INTO kv (k, v) VALUES (?, ?);", key, val)
				if err != nil {
					t.Errorf("writer %d insert error: %v", writerID, err)
				}
			}
		}(w)
	}

	for r := 0; r < numReaders; r++ {
		wg.Add(1)
		go func(readerID int) {
			defer wg.Done()
			for i := 0; i < 20; i++ {
				var count int
				_ = db.QueryRow("SELECT COUNT(*) FROM kv;").Scan(&count)
			}
		}(r)
	}

	wg.Wait()
}
