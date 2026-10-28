package indexdb

import "database/sql"

// ApplySchema creates the index tables and applies idempotent column
// migrations. Older stores (v7+) gain the columns they lack; the dropped
// callers table was a duplicate of calls and is removed so its rows are
// reclaimed by the vacuum policy.
func ApplySchema(db *sql.DB) error {
	_, err := db.Exec(`
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
	params   TEXT NOT NULL DEFAULT '',
	returns  TEXT NOT NULL DEFAULT '[]',
	confidence TEXT NOT NULL DEFAULT '',
	outer_name TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_symbols_name ON symbols(name);
CREATE INDEX IF NOT EXISTS idx_symbols_file ON symbols(file);
CREATE TABLE IF NOT EXISTS calls (
	caller     TEXT NOT NULL,
	callee     TEXT NOT NULL,
	kind       TEXT NOT NULL DEFAULT '',
	confidence TEXT NOT NULL DEFAULT '',
	synth      TEXT NOT NULL DEFAULT '',
	source_file TEXT NOT NULL DEFAULT '',
	dynamic INTEGER NOT NULL DEFAULT 0,
	from_id TEXT NOT NULL DEFAULT '',
	to_id TEXT NOT NULL DEFAULT '',
	binding TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_calls_callee ON calls(callee);
CREATE INDEX IF NOT EXISTS idx_calls_caller ON calls(caller);
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
CREATE TABLE IF NOT EXISTS file_exports (
	file    TEXT PRIMARY KEY,
	exports TEXT NOT NULL DEFAULT '[]'
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
	// The callers table duplicated the calls edges (Load never read it) and is
	// no longer written; drop it from stores created by older kern so its rows
	// and index are reclaimed by the next vacuum.
	if _, err := db.Exec("DROP TABLE IF EXISTS callers"); err != nil {
		return err
	}
	// v8 migration: the calls table gained a kind column and the communities
	// table was added. Fresh databases get both from the schema above; stores
	// written by v7 need the column added (legal with a constant default).
	if !HasColumn(db, "calls", "kind") {
		if _, err := db.Exec("ALTER TABLE calls ADD COLUMN kind TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	// v13 migration: call edges carry a confidence score. Older stores lack
	// the column; rows then default to MEDIUM on load (parseConfidence).
	if !HasColumn(db, "calls", "confidence") {
		if _, err := db.Exec("ALTER TABLE calls ADD COLUMN confidence TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	// v14 migration: call edges carry a synthesized-dispatch marker (Synth,
	// "router:net-http" etc). The JSON cache has persisted it since the
	// synthesizer feature; SQLite-primary stores written before the column
	// existed default to '' — a syntactic edge — exactly the pre-column
	// behavior, matching the struct_fields/constructors precedent.
	if !HasColumn(db, "calls", "synth") {
		if _, err := db.Exec("ALTER TABLE calls ADD COLUMN synth TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !HasColumn(db, "calls", "source_file") {
		if _, err := db.Exec("ALTER TABLE calls ADD COLUMN source_file TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !HasColumn(db, "calls", "dynamic") {
		if _, err := db.Exec("ALTER TABLE calls ADD COLUMN dynamic INTEGER NOT NULL DEFAULT 0"); err != nil {
			return err
		}
	}
	for _, migration := range []struct{ column, ddl string }{
		{"from_id", "ALTER TABLE calls ADD COLUMN from_id TEXT NOT NULL DEFAULT ''"},
		{"to_id", "ALTER TABLE calls ADD COLUMN to_id TEXT NOT NULL DEFAULT ''"},
		{"binding", "ALTER TABLE calls ADD COLUMN binding TEXT NOT NULL DEFAULT ''"},
	} {
		if !HasColumn(db, "calls", migration.column) {
			if _, err := db.Exec(migration.ddl); err != nil {
				return err
			}
		}
	}
	// v14 migration: symbols carry their declared return types and parser
	// confidence (the JSON cache has always persisted both). Older stores
	// default to '[]' / '' — the pre-column behavior — which degrades
	// update-reconstruction and confidence reporting until the next rebuild.
	if !HasColumn(db, "symbols", "returns") {
		if _, err := db.Exec("ALTER TABLE symbols ADD COLUMN returns TEXT NOT NULL DEFAULT '[]'"); err != nil {
			return err
		}
	}
	if !HasColumn(db, "symbols", "confidence") {
		if _, err := db.Exec("ALTER TABLE symbols ADD COLUMN confidence TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	if !HasColumn(db, "symbols", "outer_name") {
		if _, err := db.Exec("ALTER TABLE symbols ADD COLUMN outer_name TEXT NOT NULL DEFAULT ''"); err != nil {
			return err
		}
	}
	// v13 migration: packages carry StructFields, needed so a load-time
	// computeCallers reproduces the build-time rewriteConstructorCallees
	// results (receiver-field call chains). Older stores lack the column;
	// rows then default to no struct fields, exactly the pre-column behavior.
	if !HasColumn(db, "packages", "struct_fields") {
		if _, err := db.Exec("ALTER TABLE packages ADD COLUMN struct_fields TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}
	}
	// v13 migration: packages carry Constructors, needed so a load-time
	// computeCallers reproduces the build-time rewriteConstructorCallees
	// results (cross-package constructor-assigned receiver chains). Older
	// stores lack the column; rows then default to no constructors, exactly
	// the pre-column behavior.
	if !HasColumn(db, "packages", "constructors") {
		if _, err := db.Exec("ALTER TABLE packages ADD COLUMN constructors TEXT NOT NULL DEFAULT '{}'"); err != nil {
			return err
		}
	}
	return nil
}

// HasColumn reports whether table has a column named col (PRAGMA table_info is
// the portable way to inspect SQLite's live schema).
func HasColumn(db *sql.DB, table, col string) bool {
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
