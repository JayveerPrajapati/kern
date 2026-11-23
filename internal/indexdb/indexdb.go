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

// OptimizeFTS collapses fts5 segment tombstones (F-C4, 2026-11-22 dogfood
// campaign: TradingApp's 39 MB index for 3,539 symbols — 36 MB of it
// symbols_fts_data). Delete+reinsert churn — full saves delete all rows and
// reinsert, per-file deltas delete a file's rows and reinsert — grows fts5
// segments whose deleted entries are LIVE pages: MaybeVacuum's freelist
// trigger never sees them, so the store grows without bound. 'optimize'
// merges the segments into one compact segment, dropping tombstones; a
// following MaybeVacuum reclaims the file space. Best-effort: a failure is
// logged and never fatal to the save that called it.
func OptimizeFTS(db *sql.DB) {
	if _, err := db.Exec("INSERT INTO symbols_fts(symbols_fts) VALUES('optimize')"); err != nil {
		log.Printf("kern index: fts optimize skipped: %v", err)
	}
}

// ftsChurnThreshold is the accumulated delta-churn budget (files whose rows
// were delete+reinserted by delta saves) between fts compactions. It bounds
// the tombstone drift between full rebuilds.
const ftsChurnThreshold = 256

// MaybeCompactFTS accumulates delta churn in the meta table and compacts the
// fts index (optimize + vacuum) once it crosses ftsChurnThreshold. Called
// after ApplyDelta commits, never inside its transaction.
func MaybeCompactFTS(db *sql.DB, path string, touched int) {
	if touched <= 0 {
		return
	}
	var churn int64
	err := db.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key='fts_churn'`).Scan(&churn)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return
	}
	churn += int64(touched)
	if churn < ftsChurnThreshold {
		_, _ = db.Exec(`INSERT INTO meta(key,value) VALUES('fts_churn',?) ON CONFLICT(key) DO UPDATE SET value=excluded.value`,
			strconv.FormatInt(churn, 10))
		return
	}
	OptimizeFTS(db)
	_, _ = db.Exec(`INSERT INTO meta(key,value) VALUES('fts_churn','0') ON CONFLICT(key) DO UPDATE SET value='0'`)
	MaybeVacuum(db, path)
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
