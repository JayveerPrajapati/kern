//go:build sqlite

package index

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/cache"
	_ "modernc.org/sqlite"
)

// sqliteEnabled reports whether the SQLite store is compiled in.
func sqliteEnabled() bool { return true }

// SQLiteEnabled reports whether the SQLite persistent store is available in
// this build (built with -tags sqlite). The stub build returns false so
// callers can degrade gracefully to the JSON cache.
func SQLiteEnabled() bool { return sqliteEnabled() }

// SQLiteStore is a persistent, concurrent-safe SQLite-backed index store with
// WAL journal mode. It mirrors the JSON cache but adds true multi-process
// read/write concurrency and an FTS5 full-text index over symbols.
type SQLiteStore struct {
	db   *sql.DB
	root string
	path string
}

// sqliteDBPath returns the on-disk location for the SQLite store of root.
func sqliteDBPath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return cache.Path("db", cache.Hash([]byte(abs))+".sqlite")
}

// SQLitePath returns the on-disk location for the SQLite store of root.
func SQLitePath(root string) string { return sqliteDBPath(root) }

// OpenSQLite opens (creating if needed) the SQLite store for root and applies
// the schema. WAL journaling enables concurrent readers with a single writer.
func OpenSQLite(root string) (*SQLiteStore, error) {
	p := sqliteDBPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return nil, err
	}
	db, err := sql.Open("sqlite", "file:"+p)
	if err != nil {
		return nil, err
	}
	// WAL for concurrent access; busy_timeout so writers wait instead of
	// failing when another process holds the write lock momentarily.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA busy_timeout=5000",
		"PRAGMA synchronous=NORMAL",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, err
		}
	}
	s := &SQLiteStore{db: db, root: root, path: p}
	if err := s.applySchema(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	return s.db.Close()
}

// Path returns the database file location.
func (s *SQLiteStore) Path() string { return s.path }

func (s *SQLiteStore) applySchema() error {
	_, err := s.db.Exec(`
CREATE TABLE IF NOT EXISTS meta (
	key   TEXT PRIMARY KEY,
	value TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS symbols (
	rowid    INTEGER PRIMARY KEY,
	kind     TEXT NOT NULL,
	name     TEXT NOT NULL,
	receiver TEXT NOT NULL DEFAULT '',
	file     TEXT NOT NULL,
	line     INTEGER NOT NULL,
	"end"    INTEGER NOT NULL DEFAULT 0,
	lang     TEXT NOT NULL DEFAULT '',
	entry    INTEGER NOT NULL DEFAULT 0,
	framework TEXT NOT NULL DEFAULT '',
	route    TEXT NOT NULL DEFAULT '',
	params   TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file);
CREATE TABLE IF NOT EXISTS calls (
	caller TEXT NOT NULL,
	callee TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_calls_callee ON calls(callee);
CREATE TABLE IF NOT EXISTS callers (
	callee TEXT NOT NULL,
	caller TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_callers_callee ON callers(callee);
CREATE TABLE IF NOT EXISTS inherits (
	subtype  TEXT NOT NULL,
	base     TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_inherits_base ON inherits(base);
CREATE TABLE IF NOT EXISTS packages (
	path    TEXT PRIMARY KEY,
	name    TEXT NOT NULL DEFAULT '',
	lang    TEXT NOT NULL DEFAULT '',
	imports TEXT NOT NULL DEFAULT '[]',
	files   TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS files (
	path      TEXT PRIMARY KEY,
	hash      TEXT NOT NULL DEFAULT '',
	generated INTEGER NOT NULL DEFAULT 0
);
CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5(
	kind, name, receiver, file, params, content='', content_rowid='rowid', tokenize='unicode61'
);
`)
	return err
}

// Save persists the index to SQLite in one transaction. It is safe to call
// from multiple goroutines; SQLite serialises writers under WAL.
func (s *SQLiteStore) Save(ix *Index) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	// meta: root, version, updated_at, max_mtime
	meta := map[string]string{
		"root":       ix.Root,
		"version":    fmt.Sprintf("%d", ix.Version),
		"updated_at": ix.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"max_mtime":  fmt.Sprintf("%d", ix.MaxMtime),
		"index_kind": "symbols",
	}
	for k, v := range meta {
		if _, err := tx.Exec(
			"INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
			k, v); err != nil {
			return err
		}
	}

	if _, err := tx.Exec("DELETE FROM symbols"); err != nil {
		return err
	}
	symRow := 0
	for _, sym := range ix.Symbols {
		params, err := json.Marshal(sym.Params)
		if err != nil {
			return fmt.Errorf("marshal params for %s: %w", sym.Name, err)
		}
		entry := 0
		if sym.Entry {
			entry = 1
		}
		symRow++
		if _, err := tx.Exec(
			"INSERT INTO symbols(rowid,kind,name,receiver,file,line,\"end\",lang,entry,framework,route,params) VALUES(?,?,?,?,?,?,?,?,?,?,?,?)",
			symRow, sym.Kind, sym.Name, sym.Receiver, sym.File, sym.Line, sym.End, sym.Lang,
			entry, sym.Framework, sym.Route, string(params)); err != nil {
			return err
		}
	}

	if _, err := tx.Exec("DELETE FROM calls"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM callers"); err != nil {
		return err
	}
	stmtCalls, err := tx.Prepare("INSERT INTO calls(caller,callee) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer stmtCalls.Close()
	for caller, callees := range ix.Calls {
		for _, c := range callees {
			if _, err := stmtCalls.Exec(caller, c); err != nil {
				return err
			}
		}
	}
	stmtCallers, err := tx.Prepare("INSERT INTO callers(callee,caller) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer stmtCallers.Close()
	for callee, callers := range ix.Callers {
		for _, c := range callers {
			if _, err := stmtCallers.Exec(callee, c); err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec("DELETE FROM inherits"); err != nil {
		return err
	}
	stmtInherits, err := tx.Prepare("INSERT INTO inherits(subtype,base) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer stmtInherits.Close()
	for subtype, bases := range ix.Inherits {
		for _, b := range bases {
			if _, err := stmtInherits.Exec(subtype, b); err != nil {
				return err
			}
		}
	}

	if _, err := tx.Exec("DELETE FROM packages"); err != nil {
		return err
	}
	for path, pkg := range ix.Pkgs {
		imports, err := json.Marshal(pkg.Imports)
		if err != nil {
			return fmt.Errorf("marshal imports for %s: %w", path, err)
		}
		files, err := json.Marshal(pkg.Files)
		if err != nil {
			return fmt.Errorf("marshal files for %s: %w", path, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO packages(path,name,lang,imports,files) VALUES(?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET name=excluded.name, lang=excluded.lang, imports=excluded.imports, files=excluded.files",
			path, pkg.Name, pkg.Lang, string(imports), string(files)); err != nil {
			return err
		}
	}

	if _, err := tx.Exec("DELETE FROM files"); err != nil {
		return err
	}
	for path, h := range ix.FileHashes {
		gen := 0
		if ix.GeneratedFiles[path] {
			gen = 1
		}
		if _, err := tx.Exec(
			"INSERT INTO files(path,hash,generated) VALUES(?,?,?) ON CONFLICT(path) DO UPDATE SET hash=excluded.hash, generated=excluded.generated",
			path, h, gen); err != nil {
			return err
		}
	}

	// FTS5: rebuild the symbol full-text table row by row (contentless tables
	// do not support the 'rebuild' command).
	if _, err := tx.Exec("DELETE FROM symbols_fts"); err != nil {
		return err
	}
	ftsStmt, err := tx.Prepare("INSERT INTO symbols_fts(rowid, kind, name, receiver, file, params) VALUES(?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer ftsStmt.Close()
	for i, sym := range ix.Symbols {
		params, err := json.Marshal(sym.Params)
		if err != nil {
			return fmt.Errorf("marshal params for %s: %w", sym.Name, err)
		}
		if _, err := ftsStmt.Exec(i+1, sym.Kind, sym.Name, sym.Receiver, sym.File, string(params)); err != nil {
			return err
		}
	}

	return tx.Commit()
}

// Load reads the index back from SQLite. Returns (nil, nil) when no store
// exists for the root yet; errors on schema/version mismatch.
func (s *SQLiteStore) Load() (*Index, error) {
	var version string
	err := s.db.QueryRow("SELECT value FROM meta WHERE key='version'").Scan(&version)
	if err == sql.ErrNoRows {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if version != fmt.Sprintf("%d", indexVersion) {
		return nil, fmt.Errorf("index version %s (want %d): rebuild required", version, indexVersion)
	}

	ix := New(s.root)
	var updated string
	var maxMtime string
	var root string
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='root'").Scan(&root)
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='updated_at'").Scan(&updated)
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='max_mtime'").Scan(&maxMtime)
	ix.Root = root
	if t, err := time.Parse(time.RFC3339Nano, updated); err == nil {
		ix.UpdatedAt = t
	}
	fmt.Sscanf(maxMtime, "%d", &ix.MaxMtime)

	rows, err := s.db.Query("SELECT kind,name,receiver,file,line,\"end\",lang,entry,framework,route,params FROM symbols")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var sym Symbol
		var end, entry int
		var params string
		if err := rows.Scan(&sym.Kind, &sym.Name, &sym.Receiver, &sym.File, &sym.Line, &end,
			&sym.Lang, &entry, &sym.Framework, &sym.Route, &params); err != nil {
			return nil, err
		}
		sym.End = end
		sym.Entry = entry == 1
		if err := json.Unmarshal([]byte(params), &sym.Params); err != nil {
			return nil, fmt.Errorf("decode params for %s: %w", sym.Name, err)
		}
		ix.Symbols = append(ix.Symbols, sym)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ix.Calls = map[string][]string{}
	crows, err := s.db.Query("SELECT caller,callee FROM calls")
	if err != nil {
		return nil, err
	}
	defer crows.Close()
	for crows.Next() {
		var caller, callee string
		if err := crows.Scan(&caller, &callee); err != nil {
			return nil, err
		}
		ix.Calls[caller] = append(ix.Calls[caller], callee)
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}

	ix.Callers = map[string][]string{}
	rr, err := s.db.Query("SELECT callee,caller FROM callers")
	if err != nil {
		return nil, err
	}
	defer rr.Close()
	for rr.Next() {
		var callee, caller string
		if err := rr.Scan(&callee, &caller); err != nil {
			return nil, err
		}
		ix.Callers[callee] = append(ix.Callers[callee], caller)
	}
	if err := rr.Err(); err != nil {
		return nil, err
	}

	ix.Inherits = map[string][]string{}
	ir, err := s.db.Query("SELECT subtype,base FROM inherits")
	if err != nil {
		return nil, err
	}
	defer ir.Close()
	for ir.Next() {
		var subtype, base string
		if err := ir.Scan(&subtype, &base); err != nil {
			return nil, err
		}
		ix.Inherits[subtype] = append(ix.Inherits[subtype], base)
	}
	if err := ir.Err(); err != nil {
		return nil, err
	}

	ix.Pkgs = map[string]*Pkg{}
	pr, err := s.db.Query("SELECT path,name,lang,imports,files FROM packages")
	if err != nil {
		return nil, err
	}
	defer pr.Close()
	for pr.Next() {
		var path, name, lang, imports, files string
		if err := pr.Scan(&path, &name, &lang, &imports, &files); err != nil {
			return nil, err
		}
		pkg := &Pkg{Name: name, Path: path, Lang: lang}
		if err := json.Unmarshal([]byte(imports), &pkg.Imports); err != nil {
			return nil, fmt.Errorf("decode imports for %s: %w", path, err)
		}
		if err := json.Unmarshal([]byte(files), &pkg.Files); err != nil {
			return nil, fmt.Errorf("decode files for %s: %w", path, err)
		}
		ix.Pkgs[path] = pkg
	}
	if err := pr.Err(); err != nil {
		return nil, err
	}

	ix.FileHashes = map[string]string{}
	ix.GeneratedFiles = map[string]bool{}
	fr, err := s.db.Query("SELECT path,hash,generated FROM files")
	if err != nil {
		return nil, err
	}
	defer fr.Close()
	for fr.Next() {
		var path, hash string
		var gen int
		if err := fr.Scan(&path, &hash, &gen); err != nil {
			return nil, err
		}
		ix.FileHashes[path] = hash
		if gen == 1 {
			ix.GeneratedFiles[path] = true
		}
	}
	if err := fr.Err(); err != nil {
		return nil, err
	}

	ix.computeCallers()
	ix.reindexByFile()
	return ix, nil
}

// ftsEscape turns a user query into an FTS5 MATCH string that cannot trigger a
// syntax error, no matter what punctuation it contains. Standalone AND/OR/NOT
// operators and `column:"phrase"` / `column:word` filters are preserved;
// every other token (words, punctuation, stray quotes) is wrapped in FTS5
// double-quoted phrase form, doubling embedded quotes. Leading/trailing
// operators are dropped and consecutive ones collapsed so an input like
// "greet AND" cannot leave FTS5 with an operand-less operator.
func ftsEscape(q string) string {
	colRe := regexp.MustCompile(`^[\p{L}\p{N}_]+:("(?:[^"]|"")*"|[\p{L}\p{N}_]+)`)
	wordRe := regexp.MustCompile(`^[\p{L}\p{N}_]+`)
	phraseRe := regexp.MustCompile(`^"(?:[^"]|"")*"`)
	parts := []string{}
	rest := q
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			break
		}
		switch {
		case colRe.MatchString(rest):
			parts = append(parts, colRe.FindString(rest))
			rest = rest[len(colRe.FindString(rest)):]
		case phraseRe.MatchString(rest):
			parts = append(parts, phraseRe.FindString(rest))
			rest = rest[len(phraseRe.FindString(rest)):]
		case wordRe.MatchString(rest):
			w := wordRe.FindString(rest)
			if strings.EqualFold(w, "AND") || strings.EqualFold(w, "OR") || strings.EqualFold(w, "NOT") {
				parts = append(parts, strings.ToUpper(w))
			} else {
				parts = append(parts, `"`+w+`"`)
			}
			rest = rest[len(w):]
		default:
			// A single punctuation or quote char, quoted literally.
			parts = append(parts, `"`+strings.ReplaceAll(rest[:1], `"`, `""`)+`"`)
			rest = rest[1:]
		}
	}
	return strings.Join(sanitizeFTSOperators(parts), " ")
}

// sanitizeFTSOperators removes leading/trailing operators and collapses
// consecutive ones so the resulting FTS5 expression is always well-formed.
func sanitizeFTSOperators(parts []string) []string {
	isOp := func(s string) bool {
		return s == "AND" || s == "OR" || s == "NOT"
	}
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		if isOp(p) && len(out) > 0 && isOp(out[len(out)-1]) {
			out[len(out)-1] = p
			continue
		}
		out = append(out, p)
	}
	for len(out) > 0 && isOp(out[0]) {
		out = out[1:]
	}
	for len(out) > 0 && isOp(out[len(out)-1]) {
		out = out[:len(out)-1]
	}
	return out
}

// SearchFTS runs a full-text search over symbols via the FTS5 table. Query
// syntax follows FTS5 MATCH (e.g. "greet", "func AND greet", `file:"main.go"`).
// It returns up to limit matching symbols ranked by relevance.
func (s *SQLiteStore) SearchFTS(query string, limit int) ([]Symbol, error) {
	if strings.TrimSpace(query) == "" {
		return nil, fmt.Errorf("fts query is required")
	}
	if limit <= 0 {
		limit = 20
	}
	q := ftsEscape(query)
	if q == "" {
		// Query was only operators/punctuation — nothing to match.
		return nil, nil
	}
	rows, err := s.db.Query(`
SELECT s.kind,s.name,s.receiver,s.file,s.line,s."end",s.lang,s.entry,s.framework,s.route,s.params
FROM symbols_fts JOIN symbols s ON s.rowid = symbols_fts.rowid
WHERE symbols_fts MATCH ?
ORDER BY rank LIMIT ?`, q, limit)
	if err != nil {
		return nil, fmt.Errorf("fts query error: %w", err)
	}
	defer rows.Close()
	var out []Symbol
	for rows.Next() {
		var sym Symbol
		var end, entry int
		var params string
		if err := rows.Scan(&sym.Kind, &sym.Name, &sym.Receiver, &sym.File, &sym.Line, &end,
			&sym.Lang, &entry, &sym.Framework, &sym.Route, &params); err != nil {
			return nil, err
		}
		sym.End = end
		sym.Entry = entry == 1
		if err := json.Unmarshal([]byte(params), &sym.Params); err != nil {
			return nil, fmt.Errorf("decode params for %s: %w", sym.Name, err)
		}
		out = append(out, sym)
	}
	return out, rows.Err()
}

// SaveSQLite opens the store for root and persists ix.
func SaveSQLite(root string, ix *Index) error {
	s, err := OpenSQLite(root)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.Save(ix)
}

// LoadSQLite opens the store for root and reads the index back. Returns
// (nil, nil) when the store does not exist yet.
func LoadSQLite(root string) (*Index, error) {
	s, err := OpenSQLite(root)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	return s.Load()
}

// FTS5Search opens the SQLite store for root and runs a full-text search over
// symbols. Returns an error when sqlite is not compiled in or the store does
// not exist.
func FTS5Search(root, query string, limit int) ([]Symbol, error) {
	s, err := OpenSQLite(root)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	exists, err := storeExists(s)
	if err != nil || !exists {
		return nil, fmt.Errorf("no sqlite index for %q (run a build with -tags sqlite or use the CLI index command)", root)
	}
	return s.SearchFTS(query, limit)
}

// storeExists reports whether the store has a committed index (non-empty
// symbols table) for the root.
func storeExists(s *SQLiteStore) (bool, error) {
	var n int
	err := s.db.QueryRow("SELECT COUNT(*) FROM symbols").Scan(&n)
	if err != nil {
		return false, err
	}
	return n > 0, nil
}
