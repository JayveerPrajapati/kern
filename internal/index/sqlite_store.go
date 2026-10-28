//go:build !nosqlite

package index

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	_ "modernc.org/sqlite"

	"github.com/JayveerPrajapati/kern/internal/indexdb"
)

// sqliteEnabled reports whether the SQLite store is compiled in.
func sqliteEnabled() bool { return true }

// SQLiteEnabled reports whether the SQLite persistent store is available in
// this build. SQLite is the DEFAULT: this file is compiled in unless the
// build is tagged -tags nosqlite, which swaps in the stub (sqlite_store_stub.go)
// returning false so callers degrade gracefully to the JSON cache.
func SQLiteEnabled() bool { return sqliteEnabled() }

// SQLiteStore is a persistent, concurrent-safe SQLite-backed index store with
// WAL journal mode. It mirrors the JSON cache but adds true multi-process
// read/write concurrency and an FTS5 full-text index over symbols.
type SQLiteStore struct {
	db       *sql.DB
	root     string
	path     string
	closed   bool // set by Close; makes the WAL valve a no-op afterwards
	vacuumed bool // one bounded VACUUM per open store
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
// OpenSQLite opens (creating if needed) the SQLite index store for root.
func OpenSQLite(root string) (*SQLiteStore, error) {
	s, err := openSQLite(root)
	if err == nil {
		return s, nil
	}
	// Self-heal: the index is derived, so a corruption-class open failure
	// quarantines the store (rename, never delete — inspectable) and retries
	// once on a fresh file instead of failing every future index command.
	if indexdb.IsCorrupt(err) {
		p := sqliteDBPath(root)
		quarantine := fmt.Sprintf("%s.corrupt-%d", p, time.Now().Unix())
		if rerr := os.Rename(p, quarantine); rerr == nil {
			// Say WHY the store vanished (the quarantine used to be silent,
			// looking like a fresh repo); corruption is rare and always
			// actionable, so unlike routine build announcements this is NOT
			// gated — an operator must never miss it.
			fmt.Fprintf(os.Stderr, "[kern] corrupt index store quarantined to %s — rebuilding from source\n", quarantine)
			// Bound the quarantine's accumulation: each file is a full
			// multi-megabyte copy of the index, and a flapping corruption
			// could otherwise fill .kern/ with stale copies. Keep recent
			// ones (inspectable, per the never-delete-at-quarantine rule);
			// remove ones older than a week when a NEW quarantine succeeds.
			gcQuarantinedStores(p, quarantine, 7*24*time.Hour)
			// Drop sidecars so the fresh store adopts no stale frames.
			_ = os.Remove(p + "-wal")
			_ = os.Remove(p + "-shm")
			if s2, err2 := openSQLite(root); err2 == nil {
				return s2, nil
			}
		}
	}
	return nil, err
}

// gcQuarantinedStores removes quarantined index-store copies older than ttl,
// keeping the just-created one (and anything newer, still within its
// inspection window). Removal failures are ignored — GC is best-effort and
// must never block the rebuild.
func gcQuarantinedStores(dbPath, justCreated string, ttl time.Duration) {
	dir := filepath.Dir(dbPath)
	base := filepath.Base(dbPath)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	cutoff := time.Now().Add(-ttl)
	for _, e := range entries {
		name := e.Name()
		if e.IsDir() || !strings.HasPrefix(name, base+".corrupt-") {
			continue
		}
		full := filepath.Join(dir, name)
		if full == justCreated {
			continue
		}
		if fi, ferr := e.Info(); ferr != nil || fi.ModTime().After(cutoff) {
			continue
		}
		_ = os.Remove(full)
	}
}

// Close closes the underlying database.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	s.closed = true
	return s.db.Close()
}

func openSQLite(root string) (*SQLiteStore, error) {
	p := sqliteDBPath(root)
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return nil, err
	}
	ensureGitExclude(root)
	db, err := indexdb.OpenReadWrite(p, indexdb.CacheKiB(p, -indexdb.DefaultCacheSize))
	if err != nil {
		return nil, err
	}
	s := &SQLiteStore{db: db, root: root, path: p}
	if err := indexdb.ApplySchema(s.db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return s, nil
}

// openSQLiteReadOnly opens an EXISTING store without creating directories,
// running DDL/migrations, creating WAL files, or writing .git/info/exclude.
// Every read path uses it so a read-only checkout or mount works.
func openSQLiteReadOnly(root string) (*SQLiteStore, error) {
	p := sqliteDBPath(root)
	if _, err := os.Stat(p); err != nil {
		return nil, err
	}
	db, err := indexdb.OpenReadOnlyPreferred(p, indexdb.CacheKiB(p, -indexdb.DefaultCacheSize), indexdb.ReadOnlyEnv())
	if err != nil {
		return nil, err
	}
	return &SQLiteStore{db: db, root: root, path: p}, nil
}

// openSQLiteForRead prefers the read-only open and falls back to the
// read-write open only when the caller has not demanded read-only access.
func openSQLiteForRead(root string) (*SQLiteStore, error) {
	s, err := openSQLiteReadOnly(root)
	if err == nil {
		return s, nil
	}
	if indexdb.ReadOnlyEnv() {
		return nil, err
	}
	return openSQLite(root)
}

func (s *SQLiteStore) Path() string { return s.path }

func (s *SQLiteStore) SetMeta(key, value string) error {
	_, err := s.db.Exec(
		"INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value",
		key, value)
	return err
}

// WAL valve: autocheckpoint is off at open, so this checkpoints only once the
// WAL crosses walTrigger (PASSIVE never blocks readers/writers). Bulk builds
// checkpoint at their transaction boundary; the valve targets long-lived
// incremental writers.

// maybeVacuum reclaims free pages after a commit, only when waste is large
// and the store big enough to justify the rewrite.
func (s *SQLiteStore) maybeVacuum() {
	if s == nil || s.db == nil || s.closed || s.vacuumed {
		return
	}
	if indexdb.MaybeVacuum(s.db, s.path) {
		s.vacuumed = true
	}
}

func (s *SQLiteStore) maybeCheckpoint() error {
	if s == nil {
		return nil
	}
	return indexdb.Checkpoint(s.db, s.path, s.closed)
}

// Save persists the index to SQLite in one transaction. It is safe to call
// from multiple goroutines; SQLite serialises writers under WAL.
func (s *SQLiteStore) Save(ix *Index) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	meta := sqliteMeta(ix)
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
	// Prepare once, execute per row: re-preparing the insert on every symbol
	// was measurable overhead on large indexes. The calls/callers/inherits/
	// communities/FTS loops already use this pattern.
	stmtSymbols, err := tx.Prepare("INSERT INTO symbols(rowid,kind,name,receiver,file,line,\"end\",lang,entry,framework,route,params,returns,confidence,outer_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtSymbols.Close() }()
	symRow := 0
	for _, sym := range ix.Symbols {
		params, err := json.Marshal(sym.Params)
		if err != nil {
			return fmt.Errorf("marshal params for %s: %w", sym.Name, err)
		}
		returns, err := json.Marshal(sym.Returns)
		if err != nil {
			return fmt.Errorf("marshal returns for %s: %w", sym.Name, err)
		}
		entry := 0
		if sym.Entry {
			entry = 1
		}
		symRow++
		if _, err := stmtSymbols.Exec(symRow, sym.Kind, sym.Name, sym.Receiver, sym.File, sym.Line, sym.End, sym.Lang,
			entry, sym.Framework, sym.Route, string(params), string(returns), string(sym.Confidence), sym.Outer); err != nil {
			return err
		}
	}

	if _, err := tx.Exec("DELETE FROM calls"); err != nil {
		return err
	}
	stmtCalls, err := tx.Prepare("INSERT INTO calls(caller,callee,kind,confidence,synth,source_file,dynamic,from_id,to_id,binding) VALUES(?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtCalls.Close() }()
	for caller, callees := range ix.Calls {
		for _, ce := range callees {
			if _, err := stmtCalls.Exec(caller, ce.Target, "call", string(ce.Confidence), ce.Synth, ce.SourceFile, ce.Dynamic, ce.FromID, ce.ToID, ce.Binding); err != nil {
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
	// Prepare once, execute per row (same pattern as the symbols/calls loops).
	stmtPackages, err := tx.Prepare("INSERT INTO packages(path,name,lang,imports,files,struct_fields,constructors) VALUES(?,?,?,?,?,?,?) ON CONFLICT(path) DO UPDATE SET name=excluded.name, lang=excluded.lang, imports=excluded.imports, files=excluded.files, struct_fields=excluded.struct_fields, constructors=excluded.constructors")
	if err != nil {
		return err
	}
	defer func() { _ = stmtPackages.Close() }()
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
		if _, err := stmtPackages.Exec(path, pkg.Name, pkg.Lang, string(imports), string(files), string(structFields), string(constructors)); err != nil {
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

	if _, err := tx.Exec("DELETE FROM file_exports"); err != nil {
		return err
	}
	for file, exports := range ix.ExportsByFile {
		encoded, err := json.Marshal(exports)
		if err != nil {
			return fmt.Errorf("marshal exports for %s: %w", file, err)
		}
		if _, err := tx.Exec("INSERT INTO file_exports(file,exports) VALUES(?,?)", file, string(encoded)); err != nil {
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
	// Bounded vacuum policy: the DELETE+INSERT write cycle leaves free pages
	// behind (measured 32% of the file). Reclaim only when the waste is large,
	// and never on a small store where VACUUM's rewrite is pure overhead.
	s.maybeVacuum()
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
	// The persisted root can go stale when the whole tree (including .kern)
	// is copied or moved to a new path: the meta row still names the ORIGINAL
	// absolute path while the content itself is root-relative. The store was
	// opened at s.root — that is the authoritative location — so mirror the
	// JSON loader's reRootIndex and serve the label the load root, never the
	// stale one (2026-10-28 campaign, copied-.kern probe).
	if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != filepath.Clean(s.root) {
		ix.Root = s.root
	} else {
		ix.Root = root
	}
	var goTypes string
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='go_types'").Scan(&goTypes)
	ix.GoTypes = goTypes == "true"
	var javaCompiler string
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='java_compiler'").Scan(&javaCompiler)
	ix.JavaCompiler = javaCompiler == "true"
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='java_classpath'").Scan(&ix.JavaClasspath)
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
	// Restore the reconciliation counters (CG-P0-5) so a SQLite-loaded index
	// reports the same promoteLowEdges outcome as the build that produced it.
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='promoted_low_edges'").Scan(&ix.PromotedLowEdges)
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='unresolved_low_edges'").Scan(&ix.UnresolvedLowEdges)
	// Restore the build stats (kind/duration/workers/reused) for `kern index
	// --status`; stores written before the field existed keep the zero value.
	var buildStats string
	_ = s.db.QueryRow("SELECT value FROM meta WHERE key='build_stats'").Scan(&buildStats)
	if buildStats != "" {
		var bs BuildStats
		if err := json.Unmarshal([]byte(buildStats), &bs); err == nil && bs.Kind != "" {
			ix.BuildStats = bs
		}
	}

	rows, err := s.db.Query("SELECT kind,name,receiver,file,line,\"end\",lang,entry,framework,route,params,returns,confidence,outer_name FROM symbols")
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	for rows.Next() {
		var sym Symbol
		var end, entry int
		var params, returns, confidence string
		if err := rows.Scan(&sym.Kind, &sym.Name, &sym.Receiver, &sym.File, &sym.Line, &end,
			&sym.Lang, &entry, &sym.Framework, &sym.Route, &params, &returns, &confidence, &sym.Outer); err != nil {
			return nil, err
		}
		sym.End = end
		sym.Entry = entry == 1
		if err := json.Unmarshal([]byte(params), &sym.Params); err != nil {
			return nil, fmt.Errorf("decode params for %s: %w", sym.Name, err)
		}
		if err := json.Unmarshal([]byte(returns), &sym.Returns); err != nil {
			return nil, fmt.Errorf("decode returns for %s: %w", sym.Name, err)
		}
		sym.Confidence = Confidence(confidence)
		ix.Symbols = append(ix.Symbols, sym)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	ix.Calls = map[string][]CallEdge{}
	crows, err := s.db.Query("SELECT caller,callee,confidence,synth,source_file,dynamic,from_id,to_id,binding FROM calls")
	if err != nil {
		return nil, err
	}
	defer func() { _ = crows.Close() }()
	for crows.Next() {
		var caller, callee, confidence, synth, sourceFile, fromID, toID, binding string
		var dynamic bool
		if err := crows.Scan(&caller, &callee, &confidence, &synth, &sourceFile, &dynamic, &fromID, &toID, &binding); err != nil {
			return nil, err
		}
		ix.Calls[caller] = append(ix.Calls[caller], CallEdge{Target: callee, Confidence: parseConfidence(confidence), Synth: synth, SourceFile: sourceFile, Dynamic: dynamic, FromID: fromID, ToID: toID, Binding: binding})
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
	exportRows, err := s.db.Query("SELECT file,exports FROM file_exports")
	if err != nil {
		return nil, err
	}
	for exportRows.Next() {
		var file, encoded string
		if err := exportRows.Scan(&file, &encoded); err != nil {
			exportRows.Close()
			return nil, err
		}
		var exports []ExportEdge
		if err := json.Unmarshal([]byte(encoded), &exports); err != nil {
			exportRows.Close()
			return nil, fmt.Errorf("decode exports for %s: %w", file, err)
		}
		if ix.ExportsByFile == nil {
			ix.ExportsByFile = map[string][]ExportEdge{}
		}
		ix.ExportsByFile[file] = exports
	}
	if err := exportRows.Err(); err != nil {
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
	// ProseVocab and PrecisionByLang are derived, deterministic functions of
	// the final symbol table; the JSON cache persists them, so the SQLite
	// store rebuilds them at load to keep the two load paths equivalent.
	ix.buildProseVocab()
	ix.computePrecisionByLang()
	ix.computeCallers()
	ix.measureCallResolution()
	// SymbolsByFile is built eagerly (exported field read directly by
	// consumers after Load); the kindIdx lookup buckets are deferred to the
	// first kind-filtered Search, matching the JSON/snapshot load paths.
	ix.buildSymbolsByFile()
	return ix, nil
}

// ftsEscape turns a user query into an FTS5 MATCH string that cannot trigger a
// syntax error, no matter what punctuation it contains. Standalone AND/OR/NOT
// operators and `column:"phrase"` / `column:word` filters are preserved;
// every other token (words, punctuation, stray quotes) is wrapped in FTS5
// double-quoted phrase form, doubling embedded quotes. Leading/trailing
// operators are dropped and consecutive ones collapsed so an input like
// "greet AND" cannot leave FTS5 with an operand-less operator.
//
// ftsColRe/ftsWordRe/ftsPhraseRe are the token classifiers, compiled once at
// package init instead of per ftsEscape call.
var (
	ftsColRe    = regexp.MustCompile(`^[\p{L}\p{N}_]+:("(?:[^"]|"")*"|[\p{L}\p{N}_]+)`)
	ftsWordRe   = regexp.MustCompile(`^[\p{L}\p{N}_]+`)
	ftsPhraseRe = regexp.MustCompile(`^"(?:[^"]|"")*"`)
)

func ftsEscape(q string) string {
	parts := []string{}
	rest := q
	for len(rest) > 0 {
		rest = strings.TrimLeft(rest, " \t")
		if rest == "" {
			break
		}
		switch {
		case ftsColRe.MatchString(rest):
			parts = append(parts, ftsColRe.FindString(rest))
			rest = rest[len(ftsColRe.FindString(rest)):]
		case ftsPhraseRe.MatchString(rest):
			parts = append(parts, ftsPhraseRe.FindString(rest))
			rest = rest[len(ftsPhraseRe.FindString(rest)):]
		case ftsWordRe.MatchString(rest):
			w := ftsWordRe.FindString(rest)
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
SELECT s.kind,s.name,s.receiver,s.file,s.line,s."end",s.lang,s.entry,s.framework,s.route,s.params,s.returns,s.confidence,s.outer_name
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
		var params, returns, confidence string
		if err := rows.Scan(&sym.Kind, &sym.Name, &sym.Receiver, &sym.File, &sym.Line, &end,
			&sym.Lang, &entry, &sym.Framework, &sym.Route, &params, &returns, &confidence, &sym.Outer); err != nil {
			return nil, err
		}
		sym.End = end
		sym.Entry = entry == 1
		if err := json.Unmarshal([]byte(params), &sym.Params); err != nil {
			return nil, fmt.Errorf("decode params for %s: %w", sym.Name, err)
		}
		if err := json.Unmarshal([]byte(returns), &sym.Returns); err != nil {
			return nil, fmt.Errorf("decode returns for %s: %w", sym.Name, err)
		}
		sym.Confidence = Confidence(confidence)
		out = append(out, sym)
	}
	return out, rows.Err()
}

// SQLiteSnapshotValid reports whether a derived snapshot still describes the store.
func SQLiteSnapshotValid(root, token string, snapshotUpdated time.Time) bool {
	if token == "" {
		return false
	}
	s, err := openSQLiteForRead(root)
	if err != nil {
		return false
	}
	defer func() { _ = s.Close() }()
	return indexdb.SnapshotValid(s.db, token, snapshotUpdated)
}

// SQLiteCounts returns the persisted symbol/file/package counts with one
// metadata query instead of materializing the whole index. It is the fast path
// for `kern index where` / `kern index --status` (audit P2-1).
func SQLiteCounts(root string) (symbols, files, packages int, err error) {
	s, err := openSQLiteForRead(root)
	if err != nil {
		return 0, 0, 0, err
	}
	defer func() { _ = s.Close() }()
	return indexdb.Counts(s.db)
}

// LoadMetadata reads the store's meta table, file hashes, and counts without
// materializing symbols/calls. It is the fast path for `kern index where` and
// `kern index --status` (audit P2-1): the returned Index carries Identity,
// FileHashes and the small derived maps, so FreshnessProof and status fields
// work unchanged.
func LoadMetadata(root string) (*Metadata, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	s, err := openSQLiteForRead(abs)
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()

	meta := map[string]string{}
	rows, err := s.db.Query("SELECT key, value FROM meta")
	if err != nil {
		return nil, err
	}
	for rows.Next() {
		var k, v string
		if err := rows.Scan(&k, &v); err != nil {
			_ = rows.Close()
			return nil, err
		}
		meta[k] = v
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, err
	}
	_ = rows.Close()

	ix := &Index{
		Root:            abs,
		FileHashes:      map[string]string{},
		GeneratedFiles:  map[string]bool{},
		PrecisionByLang: map[string]string{},
		Calls:           map[string][]CallEdge{},
		Callers:         map[string][]string{},
		Pkgs:            map[string]*Pkg{},
	}
	if v, err := strconv.Atoi(meta["version"]); err == nil {
		ix.Version = v
	}
	if t, err := time.Parse(time.RFC3339Nano, meta["updated_at"]); err == nil {
		ix.UpdatedAt = t
	}
	if v, err := strconv.ParseInt(meta["max_mtime"], 10, 64); err == nil {
		ix.MaxMtime = v
	}
	if v := meta["identity"]; v != "" {
		var id IndexIdentity
		if err := json.Unmarshal([]byte(v), &id); err == nil {
			ix.Identity = &id
		}
	}
	if v := meta["precision_by_lang"]; v != "" {
		_ = json.Unmarshal([]byte(v), &ix.PrecisionByLang)
	}
	if v := meta["call_resolution"]; v != "" {
		_ = json.Unmarshal([]byte(v), &ix.CallResolution)
	}
	if v := meta["build_stats"]; v != "" {
		_ = json.Unmarshal([]byte(v), &ix.BuildStats)
	}
	ix.GoTypes = meta["go_types"] == "true"
	ix.JavaCompiler = meta["java_compiler"] == "true"
	ix.JavaClasspath = meta["java_classpath"]

	frows, err := s.db.Query("SELECT path, hash, generated FROM files")
	if err != nil {
		return nil, err
	}
	for frows.Next() {
		var path, hash string
		var generated int
		if err := frows.Scan(&path, &hash, &generated); err != nil {
			_ = frows.Close()
			return nil, err
		}
		ix.FileHashes[path] = hash
		if generated != 0 {
			ix.GeneratedFiles[path] = true
		}
	}
	if err := frows.Err(); err != nil {
		_ = frows.Close()
		return nil, err
	}
	_ = frows.Close()

	m := &Metadata{Index: ix}
	if err := s.db.QueryRow("SELECT (SELECT count(*) FROM symbols), (SELECT count(*) FROM files), (SELECT count(*) FROM packages)").Scan(&m.Symbols, &m.Files, &m.Packages); err != nil {
		return nil, err
	}
	ix.initMaps()
	ix.loadPersistedFreshness()
	return m, nil
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
// (nil, nil) when the store does not exist yet. The open is READ-ONLY
// (mode=ro/query_only, no DDL, no git writes) so a read-only checkout works.
func LoadSQLite(root string) (*Index, error) {
	if _, err := os.Stat(sqliteDBPath(root)); err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	s, err := openSQLiteForRead(root)
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
	s, err := openSQLiteForRead(root)
	if err != nil {
		return nil, err
	}
	defer func() { _ = s.Close() }()
	exists, err := indexdb.Exists(s.db)
	if err != nil || !exists {
		return nil, fmt.Errorf("no sqlite index for %q (run the CLI index command; sqlite is the default build and is disabled only with -tags nosqlite)", root)
	}
	return s.SearchFTS(query, limit)
}

// LookupSymbol performs a direct index point-query for a symbol by its Name or
// FullName (receiver-qualified, "Receiver.name"). The lookup is
// index-friendly: the predicates are separate UNION ALL branches instead of an
// OR-concat, and the receiver-qualified form is split at the input's last dot
// into an indexable `receiver = ? AND name = ?` (the split is the exact
// inverse of the concat for Go symbols, whose names never contain a dot).
// The concat expression remains as a fallback branch for exotic foreign-lang
// names that can contain dots — that branch alone may scan. The result is
// deterministic when several symbols share a name:
//
//  1. an exact name match wins over a receiver-qualified match;
//  2. among exact matches, the shortest qualified name wins (unqualified
//     symbols before methods, then the shortest receiver);
//  3. remaining ties break on name, receiver, file, then line.
//
// For unique names the behaviour is identical to a plain `WHERE name = ?`
// point-query: the single exact-match row is returned.
func (s *SQLiteStore) LookupSymbol(name string) (*Symbol, error) {
	var sym Symbol
	var end, entry int
	var params, returns, confidence string
	// Split the qualified input at its last dot: the common case
	// ("pkg.Receiver.method" style receivers) resolves to an indexable
	// receiver/name pair; inputs without a dot degenerate to a plain name
	// match already covered by branch 1.
	recvPart, namePart := "", name
	if i := strings.LastIndex(name, "."); i >= 0 {
		recvPart, namePart = name[:i], name[i+1:]
	}
	// The UNION branches each project an extra sort-key column, and the
	// ordering happens in an outer SELECT (SQLite forbids expression ORDER BY
	// terms on a compound SELECT — they must be result columns, and the scan
	// below expects exactly the 14 symbol columns).
	row := s.db.QueryRow(`
SELECT kind,name,receiver,file,line,"end",lang,entry,framework,route,params,returns,confidence,outer_name
FROM (
	SELECT kind,name,receiver,file,line,"end",lang,entry,framework,route,params,returns,confidence,outer_name,
	       0 AS prio, (receiver <> '') AS has_recv, length(receiver) AS recv_len
	FROM symbols
	WHERE name = ?
	UNION ALL
	SELECT kind,name,receiver,file,line,"end",lang,entry,framework,route,params,returns,confidence,outer_name,
	       1 AS prio, (receiver <> '') AS has_recv, length(receiver) AS recv_len
	FROM symbols
	WHERE name <> ? AND receiver = ? AND name = ?
	UNION ALL
	SELECT kind,name,receiver,file,line,"end",lang,entry,framework,route,params,returns,confidence,outer_name,
	       1 AS prio, (receiver <> '') AS has_recv, length(receiver) AS recv_len
	FROM symbols
	WHERE name <> ? AND (receiver || '.' || name) = ?
)
ORDER BY prio, has_recv, recv_len, name, receiver, file, line
LIMIT 1`, name, name, recvPart, namePart, name, name)
	if err := row.Scan(&sym.Kind, &sym.Name, &sym.Receiver, &sym.File, &sym.Line, &end,
		&sym.Lang, &entry, &sym.Framework, &sym.Route, &params, &returns, &confidence, &sym.Outer); err != nil {
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
	if err := json.Unmarshal([]byte(returns), &sym.Returns); err != nil {
		return nil, err
	}
	sym.Confidence = Confidence(confidence)
	return &sym, nil
}

// LookupCallers performs a direct B-Tree point-query for in-project callers of callee.
func (s *SQLiteStore) LookupCallers(callee string) ([]string, error) {
	rows, err := s.db.Query("SELECT DISTINCT caller FROM calls WHERE callee = ?", callee)
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
	rows, err := s.db.Query("SELECT callee, confidence, synth, source_file, dynamic, from_id, to_id, binding FROM calls WHERE caller = ?", caller)
	if err != nil {
		return nil, err
	}
	defer func() { _ = rows.Close() }()
	var edges []CallEdge
	for rows.Next() {
		var callee, confidence, synth, sourceFile, fromID, toID, binding string
		var dynamic bool
		if err := rows.Scan(&callee, &confidence, &synth, &sourceFile, &dynamic, &fromID, &toID, &binding); err == nil {
			edges = append(edges, CallEdge{
				Target:     callee,
				Confidence: parseConfidence(confidence),
				Synth:      synth,
				SourceFile: sourceFile,
				Dynamic:    dynamic,
				FromID:     fromID, ToID: toID, Binding: binding,
			})
		}
	}
	return edges, rows.Err()
}

// LookupFileSymbols performs a point-query for all symbols defined in a file.
func (s *SQLiteStore) LookupFileSymbols(file string) ([]Symbol, error) {
	rows, err := s.db.Query(`
SELECT kind,name,receiver,file,line,"end",lang,entry,framework,route,params,returns,confidence,outer_name
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
		var params, returns, confidence string
		if err := rows.Scan(&sym.Kind, &sym.Name, &sym.Receiver, &sym.File, &sym.Line, &end,
			&sym.Lang, &entry, &sym.Framework, &sym.Route, &params, &returns, &confidence, &sym.Outer); err != nil {
			return nil, err
		}
		sym.End = end
		sym.Entry = entry == 1
		// Soft-decode like the identity/build_stats meta restores: a corrupt
		// params/returns column yields nil (empty signature) — never a failed
		// lookup — but the corruption is logged, not silent. The index is
		// derived data, rebuilt on demand.
		if err := json.Unmarshal([]byte(params), &sym.Params); err != nil {
			log.Printf("kern index: corrupt params JSON for %s %s: %v", sym.Kind, sym.Name, err)
		}
		if err := json.Unmarshal([]byte(returns), &sym.Returns); err != nil {
			log.Printf("kern index: corrupt returns JSON for %s %s: %v", sym.Kind, sym.Name, err)
		}
		sym.Confidence = Confidence(confidence)
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
