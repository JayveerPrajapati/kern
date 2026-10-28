// Package indexdb owns kern's SQLite index schema, migrations, and connection
// policy. It is schema-aware but deliberately has no dependency on
// internal/index, so the index package can import it without a cycle (the
// index LOC cap forced this extraction rather than a cap raise).
//
// SQLite is the default store; the driver itself is registered by the index
// package (modernc.org/sqlite), so callers of this package run inside a binary
// that already linked it.
package indexdb

import (
	"database/sql"
	"encoding/gob"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

// CacheKiB returns the page-cache budget in KiB (positive here; callers pass
// it to the DSN as the positive size accepted by PRAGMA cache_size). A
// KERN_SQLITE_CACHE_MB override wins; otherwise the budget is
// min(fallback, db size + 8 MiB), and a fresh/unknown store keeps fallback.
func CacheKiB(dbPath string, fallback int) int {
	if mb := strings.TrimSpace(os.Getenv("KERN_SQLITE_CACHE_MB")); mb != "" {
		if n, err := strconv.Atoi(mb); err == nil && n > 0 {
			if n > 512 {
				n = 512
			}
			return n * 1024
		}
	}
	st, err := os.Stat(dbPath)
	if err != nil || st.Size() <= 0 {
		return fallback
	}
	want := int(st.Size()/1024) + 8*1024
	if want >= fallback {
		return fallback
	}
	return want
}

// DefaultCacheSize is the SQLite page-cache budget in KiB (negative values
// are KiB): 64 MiB, applied once via the DSN on every pooled connection.
const DefaultCacheSize = -64000

// OpenReadWrite opens a writable connection with the index's pragmas (WAL,
// busy timeout, memory temp store, deferred autocheckpoint). The caller owns
// schema application and directory creation.
func OpenReadWrite(dbPath string, cacheKiB int) (*sql.DB, error) {
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=wal_autocheckpoint(0)&_pragma=cache_size(-%d)", dbPath, cacheKiB)
	return openDB(dsn)
}

// OpenReadOnly opens an existing store without creating dirs, journals, or
// sidecars. immutable=true additionally forbids any journal/shm creation
// (KERN_READONLY and the read-only-mount fallback).
func OpenReadOnly(dbPath string, cacheKiB int, immutable bool) (*sql.DB, error) {
	mode := "mode=ro"
	if immutable {
		mode = "mode=ro&immutable=1"
	}
	dsn := fmt.Sprintf("file:%s?%s&_pragma=busy_timeout(5000)&_pragma=query_only(1)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-%d)", dbPath, mode, cacheKiB)
	return openDB(dsn)
}

// ReadOnlyEnv reports whether KERN_READONLY requests read-only index access.
func ReadOnlyEnv() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv("KERN_READONLY"))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

// OpenReadOnlyPreferred opens an existing store read-only, trying mode=ro
// first and falling back to immutable=1 when the shm sidecar cannot be
// created (read-only mount). preferImmutable skips straight to immutable=1.
func OpenReadOnlyPreferred(dbPath string, cacheKiB int, preferImmutable bool) (*sql.DB, error) {
	db, err := OpenReadOnly(dbPath, cacheKiB, preferImmutable)
	if err != nil && !preferImmutable {
		db, err = OpenReadOnly(dbPath, cacheKiB, true)
	}
	return db, err
}

// IsKernStore reports whether dbPath is a readable kern index with at least
// one persisted symbol or file. It intentionally requires CONTENT, not just a
// valid schema: the MCP server's own state initialization can create an empty
// .kern/index.sqlite in a scratch cwd, and counting that as a project would
// reintroduce the M#18 bootstrap. A missing, empty, corrupt, or schema-only
// store is not a project.
func IsKernStore(dbPath string) bool {
	st, err := os.Stat(dbPath)
	if err != nil || st.Size() == 0 {
		return false
	}
	db, err := OpenReadOnly(dbPath, 1024, false)
	if err != nil {
		return false
	}
	defer func() { _ = db.Close() }()
	var symbols, files int
	if err := db.QueryRow("SELECT (SELECT count(*) FROM symbols), (SELECT count(*) FROM files)").Scan(&symbols, &files); err != nil {
		return false
	}
	return symbols > 0 || files > 0
}

// WriteGobAtomic gob-encodes v to path atomically: a uniquely named temp file
// in the same directory, fsync, then rename. Readers only ever see a complete
// file.
func WriteGobAtomic(path string, v any) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".kern-gob.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	if err := gob.NewEncoder(tmp).Encode(v); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmpName, path)
}

func openDB(dsn string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(4)
	db.SetMaxIdleConns(4)
	if err := db.Ping(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

// Generation reads the derived-snapshot generation token from meta.
func Generation(db *sql.DB) (string, error) {
	var v string
	if err := db.QueryRow("SELECT value FROM meta WHERE key='generation'").Scan(&v); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return "", nil
		}
		return "", err
	}
	return v, nil
}

// SnapshotValid reports whether a derived snapshot still describes the store:
// its generation token must match meta.generation AND the snapshot must not be
// older than meta.updated_at. The timestamp leg matters because an OLDER kern
// binary writing this store updates updated_at but leaves the newer
// generation key untouched, which would otherwise keep a stale snapshot valid.
func SnapshotValid(db *sql.DB, token string, snapshotUpdated time.Time) bool {
	if token == "" {
		return false
	}
	var gen string
	if err := db.QueryRow("SELECT value FROM meta WHERE key='generation'").Scan(&gen); err != nil || gen != token {
		return false
	}
	var upd string
	if err := db.QueryRow("SELECT value FROM meta WHERE key='updated_at'").Scan(&upd); err != nil {
		return false
	}
	t, err := time.Parse(time.RFC3339Nano, upd)
	if err != nil {
		return false
	}
	return !snapshotUpdated.Before(t)
}

// Counts returns the persisted symbol/file/package counts without loading the
// index.
func Counts(db *sql.DB) (symbols, files, packages int, err error) {
	err = db.QueryRow("SELECT (SELECT count(*) FROM symbols), (SELECT count(*) FROM files), (SELECT count(*) FROM packages)").Scan(&symbols, &files, &packages)
	return
}

// Exists reports whether the store has a committed index.
func Exists(db *sql.DB) (bool, error) {
	var n int
	if err := db.QueryRow("SELECT COUNT(*) FROM symbols").Scan(&n); err != nil {
		return false, err
	}
	return n > 0, nil
}

// MaybeVacuum reclaims free pages when the waste is large (>25% of the file)
// and the store is big enough. It reports whether it ran. It must be called
// after a commit, never inside a transaction, and checkpoints the WAL the
// VACUUM produces.
func MaybeVacuum(db *sql.DB, path string) bool {
	var pages, free int64
	if err := db.QueryRow("PRAGMA page_count").Scan(&pages); err != nil {
		return false
	}
	if err := db.QueryRow("PRAGMA freelist_count").Scan(&free); err != nil {
		return false
	}
	if pages < 1000 || free*4 <= pages {
		return false
	}
	if _, err := db.Exec("VACUUM"); err != nil {
		log.Printf("kern index: vacuum skipped for %s: %v", path, err)
		return false
	}
	_, _ = db.Exec("PRAGMA wal_checkpoint(TRUNCATE)")
	return true
}

// IsCorrupt reports whether err is a SQLite corruption-class error.
func IsCorrupt(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "file is not a database") ||
		strings.Contains(msg, "notadb") ||
		strings.Contains(msg, "malformed")
}
