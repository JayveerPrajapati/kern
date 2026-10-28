package indexdb

import (
	"database/sql"
	"encoding/json"
)

// DeltaSymbol / DeltaCall / DeltaInherit / DeltaCommunity / DeltaPackage /
// DeltaBlob / DeltaFile are the row shapes of an incremental store write. They
// are plain data on purpose: internal/index builds them from its in-memory
// index, this package owns the SQL.
type DeltaSymbol struct {
	Kind, Name, Receiver, File, Lang, Framework, Route, Params, Returns, Confidence, Outer string
	Line, End, Entry                                                                       int
}

type DeltaCall struct {
	Caller, Callee, Kind, Confidence, Synth, SourceFile, FromID, ToID, Binding string
	Dynamic                                                                    int
}

type DeltaInherit struct{ Subtype, Base string }
type DeltaCommunity struct{ Symbol, Community string }
type DeltaPackage struct{ Path, Name, Lang, Imports, Files, StructFields, Constructors string }
type DeltaBlob struct{ File, JSON string }
type DeltaFile struct {
	Path, Hash string
	Generated  int
}

// Delta is an incremental store write: only the rows named here are replaced.
// Touched lists every changed or removed file; Owners lists call owners whose
// edge set differs (a changed interface recomputes dispatch edges for
// implementers in untouched files).
type Delta struct {
	Meta        map[string]string
	Touched     []string
	Changed     []string
	Removed     []string
	Owners      []string
	Symbols     []DeltaSymbol
	Calls       []DeltaCall
	Inherits    []DeltaInherit
	Communities []DeltaCommunity
	Packages    []DeltaPackage
	Imports     []DeltaBlob
	Exports     []DeltaBlob
	Files       []DeltaFile
}

// ApplyDelta writes d inside one transaction. A partial failure rolls back, so
// the caller can safely fall back to a full save.
func ApplyDelta(db *sql.DB, d *Delta) error {
	if d == nil {
		return nil
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer func() { _ = tx.Rollback() }()

	for k, v := range d.Meta {
		if _, err := tx.Exec("INSERT INTO meta(key,value) VALUES(?,?) ON CONFLICT(key) DO UPDATE SET value=excluded.value", k, v); err != nil {
			return err
		}
	}
	for _, f := range d.Touched {
		if _, err := tx.Exec("DELETE FROM symbols_fts WHERE rowid IN (SELECT rowid FROM symbols WHERE file = ?)", f); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM symbols WHERE file = ?", f); err != nil {
			return err
		}
	}
	stmtSym, err := tx.Prepare("INSERT INTO symbols(kind,name,receiver,file,line,\"end\",lang,entry,framework,route,params,returns,confidence,outer_name) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtSym.Close() }()
	stmtFTS, err := tx.Prepare("INSERT INTO symbols_fts(rowid, kind, name, receiver, file, params) VALUES(?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtFTS.Close() }()
	for _, sym := range d.Symbols {
		res, err := stmtSym.Exec(sym.Kind, sym.Name, sym.Receiver, sym.File, sym.Line, sym.End, sym.Lang,
			sym.Entry, sym.Framework, sym.Route, sym.Params, sym.Returns, sym.Confidence, sym.Outer)
		if err != nil {
			return err
		}
		rowid, err := res.LastInsertId()
		if err != nil {
			return err
		}
		if _, err := stmtFTS.Exec(rowid, sym.Kind, sym.Name, sym.Receiver, sym.File, sym.Params); err != nil {
			return err
		}
	}
	if err := applyDeltaCalls(tx, d); err != nil {
		return err
	}
	if err := rewriteGlobal(tx, d); err != nil {
		return err
	}
	// Table and column names are compile-time constants: spell each statement
	// out instead of concatenating them into the SQL. The values stay
	// parameterized either way, but concatenation is indistinguishable from
	// dynamic SQL to a lexical scanner (and to a reviewer), which is what the
	// repo's own security gate kept flagging.
	for _, f := range d.Changed {
		if _, err := tx.Exec("DELETE FROM file_imports WHERE file = ?", f); err != nil {
			return err
		}
		if _, err := tx.Exec("DELETE FROM file_exports WHERE file = ?", f); err != nil {
			return err
		}
	}
	for _, row := range d.Imports {
		if _, err := tx.Exec("INSERT INTO file_imports(file,imports) VALUES(?,?)", row.File, row.JSON); err != nil {
			return err
		}
	}
	for _, row := range d.Exports {
		if _, err := tx.Exec("INSERT INTO file_exports(file,exports) VALUES(?,?)", row.File, row.JSON); err != nil {
			return err
		}
	}
	for _, f := range d.Touched {
		if _, err := tx.Exec("DELETE FROM files WHERE path = ?", f); err != nil {
			return err
		}
	}
	for _, f := range d.Files {
		if _, err := tx.Exec("INSERT INTO files(path,hash,generated) VALUES(?,?,?)", f.Path, f.Hash, f.Generated); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func applyDeltaCalls(tx *sql.Tx, d *Delta) error {
	if _, err := tx.Exec("CREATE TEMP TABLE IF NOT EXISTS delta_owners(o TEXT PRIMARY KEY)"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM delta_owners"); err != nil {
		return err
	}
	for _, owner := range d.Owners {
		if _, err := tx.Exec("INSERT OR IGNORE INTO delta_owners(o) VALUES(?)", owner); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("CREATE TEMP TABLE IF NOT EXISTS delta_files(f TEXT PRIMARY KEY)"); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM delta_files"); err != nil {
		return err
	}
	for _, f := range d.Touched {
		if _, err := tx.Exec("INSERT OR IGNORE INTO delta_files(f) VALUES(?)", f); err != nil {
			return err
		}
	}
	if _, err := tx.Exec("DELETE FROM calls WHERE caller IN (SELECT o FROM delta_owners) OR source_file IN (SELECT f FROM delta_files)"); err != nil {
		return err
	}
	stmt, err := tx.Prepare("INSERT INTO calls(caller,callee,kind,confidence,synth,source_file,dynamic,from_id,to_id,binding) VALUES(?,?,?,?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmt.Close() }()
	for _, c := range d.Calls {
		if _, err := stmt.Exec(c.Caller, c.Callee, c.Kind, c.Confidence, c.Synth, c.SourceFile, c.Dynamic, c.FromID, c.ToID, c.Binding); err != nil {
			return err
		}
	}
	return nil
}

func rewriteGlobal(tx *sql.Tx, d *Delta) error {
	// Fixed statements, no concatenation (see the comment in Apply).
	for _, stmt := range []string{
		"DELETE FROM inherits",
		"DELETE FROM communities",
		"DELETE FROM packages",
	} {
		if _, err := tx.Exec(stmt); err != nil {
			return err
		}
	}
	stmtInh, err := tx.Prepare("INSERT INTO inherits(subtype,base) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtInh.Close() }()
	for _, r := range d.Inherits {
		if _, err := stmtInh.Exec(r.Subtype, r.Base); err != nil {
			return err
		}
	}
	stmtCom, err := tx.Prepare("INSERT INTO communities(symbol,community) VALUES(?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtCom.Close() }()
	for _, r := range d.Communities {
		if _, err := stmtCom.Exec(r.Symbol, r.Community); err != nil {
			return err
		}
	}
	stmtPkg, err := tx.Prepare("INSERT INTO packages(path,name,lang,imports,files,struct_fields,constructors) VALUES(?,?,?,?,?,?,?)")
	if err != nil {
		return err
	}
	defer func() { _ = stmtPkg.Close() }()
	for _, p := range d.Packages {
		if _, err := stmtPkg.Exec(p.Path, p.Name, p.Lang, p.Imports, p.Files, p.StructFields, p.Constructors); err != nil {
			return err
		}
	}
	return nil
}

// MarshalJSONBlob encodes v as a JSON string for a blob column, returning ""
// on failure (a nil slice encodes as "null", which Load decodes back to nil).
func MarshalJSONBlob(v any) string {
	data, err := json.Marshal(v)
	if err != nil {
		return ""
	}
	return string(data)
}
