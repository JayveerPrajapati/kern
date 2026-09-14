//go:build !nosqlite

package index

import (
	"database/sql"
	"encoding/binary"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

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
	db     *sql.DB
	root   string
	path   string
	closed bool // set by Close; makes the WAL valve a no-op afterwards
}

// sqliteDBPath returns the on-disk location for the SQLite store of root.
// The index lives per-project under <root>/.kern/ so it is portable,
// self-contained, and never pollutes a global cache.
func sqliteDBPath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return filepath.Join(abs, ".kern", "index.sqlite")
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
	ensureGitExclude(root)
	dsn := fmt.Sprintf("file:%s?_pragma=busy_timeout(5000)&_pragma=journal_mode(WAL)&_pragma=synchronous(NORMAL)&_pragma=temp_store(MEMORY)&_pragma=cache_size(-20000)", p)
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, err
	}
	// WAL for concurrent access; busy_timeout so writers wait instead of
	// failing when another process holds the write lock momentarily.
	//
	// wal_autocheckpoint=0 defers SQLite's default autocheckpoint (1000
	// pages) so WAL growth is managed by the valve below. Bulk builds write
	// inside a single transaction and are checkpointed by the valve at the
	// transaction boundary; the valve targets the incremental
	// watch-daemon/MCP write pattern, where the coarse 1000-page default
	// would otherwise checkpoint on every commit of a long-lived process.
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL;",
		"PRAGMA synchronous=NORMAL;",
		"PRAGMA busy_timeout=5000;",
		"PRAGMA temp_store=MEMORY;",
		"PRAGMA cache_size=-64000;",
		"PRAGMA mmap_size=268435456;", // 256MB memory mapped I/O
		"PRAGMA wal_autocheckpoint=0;",
	} {
		if _, err := db.Exec(pragma); err != nil {
			_ = db.Close()
			return nil, err
		}
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	s := &SQLiteStore{db: db, root: root, path: p}
	if err := s.applySchema(); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// Close closes the underlying database.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.closed = true
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
	caller     TEXT NOT NULL,
	callee     TEXT NOT NULL,
	kind       TEXT NOT NULL DEFAULT '',
	confidence TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_calls_callee ON calls(callee);
CREATE INDEX IF NOT EXISTS idx_calls_caller ON calls(caller);
CREATE TABLE IF NOT EXISTS callers (
	callee TEXT NOT NULL,
	caller TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_callers_callee ON callers(callee);
CREATE TABLE IF NOT EXISTS communities (
	symbol    TEXT PRIMARY KEY,
	community TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS idx_communities_label ON communities(community);
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
	files   TEXT NOT NULL DEFAULT '[]',
	struct_fields TEXT NOT NULL DEFAULT '{}',
constructors TEXT NOT NULL DEFAULT '{}'
);
CREATE TABLE IF NOT EXISTS file_imports (
	file    TEXT PRIMARY KEY,
	imports TEXT NOT NULL DEFAULT '[]'
);
CREATE TABLE IF NOT EXISTS files (
	path      TEXT PRIMARY KEY,
	hash      TEXT NOT NULL DEFAULT '',
	generated INTEGER NOT NULL DEFAULT 0
);
CREATE VIRTUAL TABLE IF NOT EXISTS symbols_fts USING fts5(
	kind, name, receiver, file, params, tokenize='unicode61'
);
`)
	if err != nil {
		return err
	}
	// v8 migration: the calls table gained a kind column and the communities
	// table was added. Fresh databases get both from the schema above; stores
	// written by v7 need the column added (legal with a constant default).
	if !storeHasColumn(s.db, "calls", "kind") {
		if _, err := s.db.Exec("ALTER TABLE calls ADD COLUMN kind TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	// v13 migration: call edges carry a confidence score. Older stores lack
	// the column; rows then default to MEDIUM on load (parseConfidence).
	if !storeHasColumn(s.db, "calls", "confidence") {
		if _, err := s.db.Exec("ALTER TABLE calls ADD COLUMN confidence TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	// v13 migration: packages carry StructFields, needed so a load-time
	// computeCallers reproduces the build-time rewriteConstructorCallees
	// results (receiver-field call chains). Older stores lack the column;
	// rows then default to no struct fields, exactly the pre-column behavior.
	if !storeHasColumn(s.db, "packages", "struct_fields") {
		if _, err := s.db.Exec("ALTER TABLE packages ADD COLUMN struct_fields TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}
	}
	// v13 migration: packages carry Constructors, needed so a load-time
	// computeCallers reproduces the build-time rewriteConstructorCallees
	// results (cross-package constructor-assigned receiver chains). Older
	// stores lack the column; rows then default to no constructors, exactly
	// the pre-column behavior.
	if !storeHasColumn(s.db, "packages", "constructors") {
		if _, err := s.db.Exec("ALTER TABLE packages ADD COLUMN constructors TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}
	}
	return nil
}

// storeHasColumn reports whether table has a column named col (PRAGMA
// table_info is the portable way to inspect SQLite's live schema).
func storeHasColumn(db *sql.DB, table, col string) bool {
	rows, err := db.Query("PRAGMA table_info(" + table + ")")
	if err != nil {
		return false
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var cid, notnull, pk int
		var name, typ string
		var dflt sql.NullString
		if err := rows.Scan(&cid, &name, &typ, &notnull, &dflt, &pk); err != nil {
			return false
		}
		if name == col {
			return true
		}
	}
	return false
}

// WAL valve: bounds the write-ahead log for the incremental
// watch-daemon/MCP write pattern (many small writes over a long-lived
// process). wal_autocheckpoint is disabled at open, so SQLite's default
// 1000-page autocheckpoint never fires on its own; the valve below
// checkpoints only once the WAL crosses walTrigger, and PASSIVE never
// blocks readers or writers. Bulk builds wrap the whole index in one
// transaction and are checkpointed by the valve at that transaction
// boundary; the valve targets the incremental pattern, where the coarse
// default would otherwise checkpoint on every commit.
const (
	// walSoftCap is the target ceiling for the WAL: 8 MiB. That is far
	// below the 100 MiB sandbox snapshot cap and the JSON index scale, so
	// the WAL stays a small fraction of the store.
	walSoftCap = 8 << 20 // 8 MiB
	// walTrigger is the size at which maybeCheckpoint fires a passive
	// checkpoint: 2 x walSoftCap. Crossing it is rare (only after a burst
	// of incremental writes), which is what keeps the per-write check cheap.
	walTrigger = 2 * walSoftCap // 16 MiB
)

// walLiveFrames returns the number of uncheckpointed WAL frames by reading
// the WAL-index header in the -shm file (mxFrame at offset 16 minus
// nBackfill at offset 96; layout per https://www.sqlite.org/walformat.html,
// frozen since SQLite 3.7.0). modernc.org/sqlite does not expose
// PRAGMA wal_pages/wal_size, so this is the only side-effect-free live-WAL
// probe the store has. It returns a negative value when the file is missing
// or unreadable, which callers treat as "checkpoint to be safe".
func walLiveFrames(storePath string) int64 {
	f, err := os.Open(storePath + "-shm")
	if err != nil {
		return -1
	}
	defer func() { _ = f.Close() }()
	var hdr [100]byte
	if _, err := f.ReadAt(hdr[:], 0); err != nil {
		return -1
	}
	mx := int64(binary.LittleEndian.Uint32(hdr[16:20]))
	nb := int64(binary.LittleEndian.Uint32(hdr[96:100]))
	if mx < nb {
		return -1 // torn read (concurrent checkpoint): treat as unreadable
	}
	return mx - nb
}

// maybeCheckpoint runs a passive WAL checkpoint once the write-ahead log
// exceeds walTrigger (2 x walSoftCap). It is called at the end of every
// mutating operation, after the write commits.
//
// PASSIVE checkpoints as much as it can without blocking readers or
// writers; when another connection (e.g. a separate watch-daemon or MCP
// process) holds a read lock it returns busy and defers the flush to the
// next write — that is the backpressure mechanism, and the threshold
// guarantees the check itself is rare. A nil or already-closed store is a
// no-op.
func (s *SQLiteStore) maybeCheckpoint() error {
	if s == nil || s.db == nil || s.closed {
		return nil
	}
	// Cheap disk gate: the live WAL can exceed walTrigger only once the WAL
	// file's high-water size reaches it (SQLite never truncates the -wal
	// file while the store is open), so a small file proves the WAL is
	// small too — no shm read, no DB round trip on the common path.
	st, err := os.Stat(s.path + "-wal")
	if err != nil {
		return nil // no WAL file yet (fresh store)
	}
	if st.Size() < walTrigger {
		return nil
	}
	// The file is big, but it may be a high-water mark with a small live
	// WAL (all frames already backfilled). Measure before checkpointing so
	// the flush stays rare.
	if frames := walLiveFrames(s.path); frames >= 0 {
		var pageSize int64
		if err := s.db.QueryRow("PRAGMA page_size").Scan(&pageSize); err != nil {
			return err
		}
		if frames*pageSize <= walTrigger {
			return nil
		}
	}
	// Live WAL crossed the trigger (or could not be measured — fail safe
	// toward a bounded WAL). PASSIVE never blocks; a busy result means
	// another connection holds the WAL and the next write re-checks.
	_, err = s.db.Exec("PRAGMA wal_checkpoint(PASSIVE)")
	return err
}

// Save persists the index to SQLite in one transaction. It is safe to call
// from multiple goroutines; SQLite serialises writers under WAL.
func (s *SQLiteStore) Save(ix *Index) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	// meta: root, version, updated_at, max_mtime, identity
	meta := map[string]string{
		"root":       ix.Root,
		"version":    fmt.Sprintf("%d", ix.Version),
		"updated_at": ix.UpdatedAt.UTC().Format(time.RFC3339Nano),
		"max_mtime":  fmt.Sprintf("%d", ix.MaxMtime),
		"index_kind": "symbols",
	}
	// Persist the content-addressed identity (best-effort) so SQLite-loaded
	// indexes get the same freshness proof as JSON-loaded ones.
	if ix.Identity != nil {
		if idData, err := json.Marshal(ix.Identity); err == nil {
			meta["identity"] = string(idData)
		}
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
	stmtCalls, err := tx.Prepare("INSERT INTO calls(caller,callee,kind,confidence) VALUES(?,?,?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtCalls.Close() }()
	for caller, callees := range ix.Calls {
		for _, ce := range callees {
			if _, err := stmtCalls.Exec(caller, ce.Target, "call", string(ce.Confidence)); err != nil {
				return err
			}
		}
	}
	stmtCallers, err := tx.Prepare("INSERT INTO callers(callee,caller) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtCallers.Close() }()
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
	defer func() { _ = stmtInherits.Close() }()
	for subtype, bases := range ix.Inherits {
		for _, b := range bases {
			if _, err := stmtInherits.Exec(subtype, b); err != nil {
				return err
			}
		}
	}

	// Communities: persist the label-propagation result so graph consumers
	// can read membership without recomputing it.
	if _, err := tx.Exec("DELETE FROM communities"); err != nil {
		return err
	}
	stmtCommunities, err := tx.Prepare("INSERT INTO communities(symbol,community) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtCommunities.Close() }()
	for sym, comm := range ix.CommunityLabels() {
		if _, err := stmtCommunities.Exec(sym, comm); err != nil {
			return err
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
		structFields, err := json.Marshal(pkg.StructFields)
		if err != nil {
			return fmt.Errorf("marshal struct fields for %s: %w", path, err)
		}
		constructors, err := json.Marshal(pkg.Constructors)
		if err != nil {
			return fmt.Errorf("marshal constructors for %s: %w", path, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO packages(path,name,lang,imports,files,struct_fields,constructors) VALUES(?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET name=excluded.name, lang=excluded.lang, imports=excluded.imports, files=excluded.files, struct_fields=excluded.struct_fields, constructors=excluded.constructors",
			path, pkg.Name, pkg.Lang, string(imports), string(files), string(structFields), string(constructors)); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DELETE FROM file_imports"); err != nil {
		return err
	}
	for file, imps := range ix.ImportsByFile {
		fileImports, err := json.Marshal(imps)
		if err != nil {
			return fmt.Errorf("marshal imports for %s: %w", file, err)
		}
		if _, err := tx.Exec(
			"INSERT INTO file_imports(file,imports) VALUES(?,?) ON CONFLICT(file) DO UPDATE SET imports=excluded.imports",
			file, string(fileImports)); err != nil {
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

	// FTS5: rebuild the symbol full-text table row by row. The table is a
	// regular (non-contentless) FTS5 table, so DELETE works and clears all
	// rows before re-inserting.
	if _, err := tx.Exec("DELETE FROM symbols_fts"); err != nil {
		return err
	}
	ftsStmt, err := tx.Prepare("INSERT INTO symbols_fts(rowid, kind, name, receiver, file, params) VALUES(?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = ftsStmt.Close() }()
	for i, sym := range ix.Symbols {
		params, err := json.Marshal(sym.Params)
		if err != nil {
			return fmt.Errorf("marshal params for %s: %w", sym.Name, err)
		}
		if _, err := ftsStmt.Exec(i+1, sym.Kind, sym.Name, sym.Receiver, sym.File, string(params)); err != nil {
			return err
		}
	}

	if err := tx.Commit(); err != nil {
		return err
	}
	// WAL valve: the write committed; checkpoint once the WAL has crossed
	// walTrigger. This is the single post-write valve point — Save is the
	// store's only mutating operation, so nothing is missed and the check
	// is never inside a per-row loop.
	return s.maybeCheckpoint()
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
	_, _ = fmt.Sscanf(maxMtime, "%d", &ix.MaxMtime)
	// Restore the content-addressed identity if the store has one; indexes
	// written before identity existed get a nil Identity and fail closed.
	var identity string
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='identity'").Scan(&identity)
	if identity != "" {
		var id IndexIdentity
		if err := json.Unmarshal([]byte(identity), &id); err == nil {
			ix.Identity = &id
		}
	}

	rows, err := s.db.Query("SELECT kind,name,receiver,file,line,\"end\",lang,entry,framework,route,params FROM symbols")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
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

	ix.Calls = map[string][]CallEdge{}
	crows, err := s.db.Query("SELECT caller,callee,confidence FROM calls")
	if err != nil {
		return nil, err
	}
	defer func() { _ = crows.Close() }()
	for crows.Next() {
		var caller, callee, confidence string
		if err := crows.Scan(&caller, &callee, &confidence); err != nil {
			return nil, err
		}
		ix.Calls[caller] = append(ix.Calls[caller], CallEdge{Target: callee, Confidence: parseConfidence(confidence)})
	}
	if err := crows.Err(); err != nil {
		return nil, err
	}

	ix.Callers = map[string][]string{}
	// The callers TABLE is deliberately not read: computeCallers below
	// rebuilds ix.Callers (plus AliasCallers and InheritedBy) from the
	// calls/inherits rows, unconditionally replacing the map, so reading the
	// persisted copy back was dead work — a full table scan whose result was
	// discarded.

	ix.Inherits = map[string][]string{}
	ir, err := s.db.Query("SELECT subtype,base FROM inherits")
	if err != nil {
		return nil, err
	}
	defer func() { _ = ir.Close() }()
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

	ix.Communities = map[string]string{}
	com, err := s.db.Query("SELECT symbol,community FROM communities")
	if err != nil {
		return nil, err
	}
	defer func() { _ = com.Close() }()
	for com.Next() {
		var sym, community string
		if err := com.Scan(&sym, &community); err != nil {
			return nil, err
		}
		ix.Communities[sym] = community
	}
	if err := com.Err(); err != nil {
		return nil, err
	}

	ix.Pkgs = map[string]*Pkg{}
	pr, err := s.db.Query("SELECT path,name,lang,imports,files,struct_fields,constructors FROM packages")
	if err != nil {
		return nil, err
	}
	defer func() { _ = pr.Close() }()
	for pr.Next() {
		var path, name, lang, imports, files, structFields, constructors string
		if err := pr.Scan(&path, &name, &lang, &imports, &files, &structFields, &constructors); err != nil {
			return nil, err
		}
		pkg := &Pkg{Name: name, Path: path, Lang: lang}
		if err := json.Unmarshal([]byte(imports), &pkg.Imports); err != nil {
			return nil, fmt.Errorf("decode imports for %s: %w", path, err)
		}
		if err := json.Unmarshal([]byte(files), &pkg.Files); err != nil {
			return nil, fmt.Errorf("decode files for %s: %w", path, err)
		}
		// StructFields is required for the load-time computeCallers to
		// reproduce the build-time rewriteConstructorCallees results; older
		// stores default to '{}' which decodes to the same empty map the
		// pre-column load produced.
		if err := json.Unmarshal([]byte(structFields), &pkg.StructFields); err != nil {
			return nil, fmt.Errorf("decode struct fields for %s: %w", path, err)
		}
		// Constructors feeds the same rewrite for cross-package
		// constructor-assigned receiver chains; '{}' decodes to the same
		// empty map the pre-column load produced.
		if err := json.Unmarshal([]byte(constructors), &pkg.Constructors); err != nil {
			return nil, fmt.Errorf("decode constructors for %s: %w", path, err)
		}
		ix.Pkgs[path] = pkg
	}
	if err := pr.Err(); err != nil {
		return nil, err
	}
	ix.ImportsByFile = map[string][]ImportEdge{}
	fir, err := s.db.Query("SELECT file,imports FROM file_imports")
	if err != nil {
		return nil, err
	}
	for fir.Next() {
		var file, imports string
		if err := fir.Scan(&file, &imports); err != nil {
			return nil, err
		}
		var imps []ImportEdge
		if err := json.Unmarshal([]byte(imports), &imps); err != nil {
			return nil, fmt.Errorf("decode imports for %s: %w", file, err)
		}
		ix.ImportsByFile[file] = imps
	}
	if err := fir.Err(); err != nil {
		return nil, err
	}

	ix.FileHashes = map[string]string{}
	ix.GeneratedFiles = map[string]bool{}
	fr, err := s.db.Query("SELECT path,hash,generated FROM files")
	if err != nil {
		return nil, err
	}
	defer func() { _ = fr.Close() }()
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

	// computeCallers resolves each dotted call edge via symbolsFor, which is
	// a linear scan over every symbol unless buildSymbolIndex has run. Every
	// other finalize path (Build, Update) pairs the two; without this call a
	// load of ~13k symbols with ~30k edges degraded to O(edges x symbols) —
	// measured at ~6.8s on the kern repo, ~100x the JSON load of the same
	// data. buildSymbolIndex is O(symbols) and pays for itself immediately.
	ix.buildSymbolIndex()
	ix.computeCallers()
	ix.measureCallResolution()
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
	defer func() { _ = rows.Close() }()
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
	defer func() { _ = s.Close() }()
	return s.Save(ix)
}

// LoadSQLite opens the store for root and reads the index back. Returns
// (nil, nil) when the store does not exist yet.
func LoadSQLite(root string) (*Index, error) {
	s, err := OpenSQLite(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
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
	defer func() { _ = s.Close() }()
	exists, err := storeExists(s)
	if err != nil || !exists {
		return nil, fmt.Errorf("no sqlite index for %q (run a build with -tags sqlite or use the CLI index command)", root)
	}
	return s.SearchFTS(query, limit)
}

// LookupSymbol performs a direct index point-query for a symbol by its Name or FullName.
func (s *SQLiteStore) LookupSymbol(name string) (*Symbol, error) {
	var sym Symbol
	var end, entry int
	var params string
	row := s.db.QueryRow(`
SELECT kind,name,receiver,file,line,"end",lang,entry,framework,route,params
FROM symbols
WHERE name = ? OR (receiver || '.' || name) = ?
LIMIT 1`, name, name)
	if err := row.Scan(&sym.Kind, &sym.Name, &sym.Receiver, &sym.File, &sym.Line, &end,
		&sym.Lang, &entry, &sym.Framework, &sym.Route, &params); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, err
	}
	sym.End = end
	sym.Entry = entry == 1
	if err := json.Unmarshal([]byte(params), &sym.Params); err != nil {
		return nil, err
	}
	return &sym, nil
}

// LookupCallers performs a direct B-Tree point-query for in-project callers of callee.
func (s *SQLiteStore) LookupCallers(callee string) ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT caller FROM calls WHERE callee = ? UNION SELECT caller FROM callers WHERE callee = ?", callee, callee)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var callers []string
	for rows.Next() {
		var c string
		if err := rows.Scan(&c); err == nil && c != "" {
			callers = append(callers, c)
		}
	}
	return callers, rows.Err()
}

// LookupCalls performs a direct point-query for outgoing call edges from caller.
func (s *SQLiteStore) LookupCalls(caller string) ([]CallEdge, error) {
	rows, err := s.db.Query("SELECT callee, confidence FROM calls WHERE caller = ?", caller)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var edges []CallEdge
	for rows.Next() {
		var callee, confidence string
		if err := rows.Scan(&callee, &confidence); err == nil {
			edges = append(edges, CallEdge{
				Target:     callee,
				Confidence: parseConfidence(confidence),
			})
		}
	}
	return edges, rows.Err()
}

// LookupFileSymbols performs a point-query for all symbols defined in a file.
func (s *SQLiteStore) LookupFileSymbols(file string) ([]Symbol, error) {
	rows, err := s.db.Query(`
SELECT kind,name,receiver,file,line,"end",lang,entry,framework,route,params
FROM symbols
WHERE file = ?
ORDER BY line ASC`, file)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var syms []Symbol
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
		_ = json.Unmarshal([]byte(params), &sym.Params)
		syms = append(syms, sym)
	}
	return syms, rows.Err()
}

// LookupInherits returns the base types/interfaces that subtype inherits or implements.
func (s *SQLiteStore) LookupInherits(subtype string) ([]string, error) {
	rows, err := s.db.Query("SELECT base FROM inherits WHERE subtype = ?", subtype)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var bases []string
	for rows.Next() {
		var b string
		if err := rows.Scan(&b); err == nil {
			bases = append(bases, b)
		}
	}
	return bases, rows.Err()
}

// LookupInheritedBy returns the subtypes that extend or implement base.
func (s *SQLiteStore) LookupInheritedBy(base string) ([]string, error) {
	rows, err := s.db.Query("SELECT subtype FROM inherits WHERE base = ?", base)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var subtypes []string
	for rows.Next() {
		var sub string
		if err := rows.Scan(&sub); err == nil {
			subtypes = append(subtypes, sub)
		}
	}
	return subtypes, rows.Err()
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
