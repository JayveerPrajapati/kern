// Binary snapshot cache for the persisted JSON index (Rec P0-3).
//
// The CLI pays a per-invocation floor of ~300ms parsing <root>/.kern/index.json
// (a ~10MB JSON document) on every index-backed command. This file adds a
// derived gob snapshot written alongside the JSON at Save time and preferred
// at load time when it is provably fresh — same content, ~5-10x cheaper
// decode, zero new dependencies (encoding/gob is stdlib).
//
// Name note: this is the *binary cache* snapshot. The blueprint graph
// snapshot (GraphSnapshot / Index.Snapshot / LoadSnapshot / VerifySnapshot
// in snapshot.go) is a different, unrelated feature — a lightweight graph
// fingerprint for the verify walk. Both coexist.
//
// Invariants:
//   - The JSON cache is the nosqlite/legacy format; Load falls back to it
//     whenever the snapshot is missing, stale or undecodable.
//   - Freshness compares the header against the current store (JSON stat pair,
//     or the SQLite generation token), so a stale snapshot can never win.
//   - A corrupt/foreign/oversized snapshot degrades to the JSON path.
//
// The SQLite store (default build; disabled only with -tags nosqlite) remains
// the richer store; this snapshot exists specifically so the default build
// stops paying the JSON parse cost on every CLI invocation.
package index

import (
	"bytes"
	"encoding/gob"
	"github.com/JayveerPrajapati/kern/internal/indexdb"
	"os"
)

// binSnapshotMagic identifies the file as a kern binary index snapshot;
// binSnapshotVersion is the snapshot format version (independent of
// indexVersion — bump only when the wrapper/header layout changes).
const (
	binSnapshotMagic   = "KERNSNAP"
	binSnapshotVersion = 2
)

// binSnapshotHeader records the source store's identity so a stale snapshot
// (written before a newer store) is rejected by comparison, never by trust.
// All fields exported for gob.
//
// Store + Token are the robust freshness proof: "json" snapshots fall back to
// the JSON stat pair, while "sqlite" snapshots carry the generation token
// persisted in the SQLite meta table (identity + updated-at + row counts), so
// coarse-mtime filesystems, cp -p and rsync -t cannot let a stale snapshot win.
type binSnapshotHeader struct {
	Magic        string
	Version      int
	Store        string // "json" | "sqlite"
	Token        string // store generation token (sha256); empty for legacy JSON
	IndexVersion int
	JSONSize     int64
	JSONModNS    int64
	SQLiteSize   int64
	SQLiteModNS  int64
}

// binSnapshot is the on-disk snapshot: the header plus the full index.
type binSnapshot struct {
	H binSnapshotHeader
	I *Index
}

// binSnapshotPathFor returns the snapshot path for a given index.json path:
// the JSON path with ".snap" appended, in the same directory.
func binSnapshotPathFor(jsonPath string) string {
	return jsonPath + ".snap"
}

// writeBinSnapshot writes the gob snapshot for ix alongside jsonPath. Best
// effort: the caller (Save) treats failure as non-fatal — the snapshot is a
// cache, and the JSON remains canonical. Writes are atomic (temp file +
// rename) so a concurrent reader never sees a half-written snapshot.
func writeBinSnapshot(ix *Index, jsonPath string) error {
	st, err := os.Stat(jsonPath)
	if err != nil {
		return err
	}
	return writeBinSnapshotFile(binSnapshot{
		H: binSnapshotHeader{
			Magic:        binSnapshotMagic,
			Version:      binSnapshotVersion,
			Store:        "json",
			IndexVersion: ix.Version,
			JSONSize:     st.Size(),
			JSONModNS:    st.ModTime().UnixNano(),
		},
		I: snapshotCopy(ix),
	}, jsonPath)
}

// writeSQLiteBinSnapshot writes the derived fast snapshot for the
// SQLite-primary store (header token proves freshness without a full decode).
func writeSQLiteBinSnapshot(ix *Index, root string) error {
	st, err := os.Stat(SQLitePath(root))
	if err != nil {
		return err
	}
	return writeBinSnapshotFile(binSnapshot{
		H: binSnapshotHeader{
			Magic:        binSnapshotMagic,
			Version:      binSnapshotVersion,
			Store:        "sqlite",
			Token:        indexGeneration(ix),
			IndexVersion: ix.Version,
			SQLiteSize:   st.Size(),
			SQLiteModNS:  st.ModTime().UnixNano(),
		},
		I: snapshotCopy(ix),
	}, StorePath(root))
}

// snapshotCopy clears SymbolsByFile before encoding (Load rebuilds it).
func snapshotCopy(ix *Index) *Index {
	if ix == nil {
		return nil
	}
	cp := *ix
	cp.SymbolsByFile = nil
	return &cp
}

func indexGeneration(ix *Index) string {
	if ix == nil {
		return ""
	}
	contentRoot := ""
	if ix.Identity != nil {
		// TreeOID/GitCommit are excluded: the commit-label self-heal rewrites
		// GitCommit without changing indexed content.
		contentRoot = ix.Identity.ContentRoot
	}
	return indexdb.GenerationToken(ix.Version, ix.UpdatedAt, len(ix.Symbols), len(ix.Calls), len(ix.FileHashes), contentRoot)
}

func writeBinSnapshotFile(snap binSnapshot, jsonPath string) error {
	return indexdb.WriteGobAtomic(binSnapshotPathFor(jsonPath), &snap)
}

// loadBinSnapshot returns the index from the binary snapshot when it exists
// and is provably fresh (header matches the current index.json stat);
// ok=false on any failure — missing, stale, corrupt, or version-mismatched —
// so the caller falls back to the JSON path. The returned index still needs
// the same post-processing the JSON loader applies (initMaps, reindexByFile,
// buildSymbolIndex as appropriate), and its Version field is checked by the
// caller exactly as the JSON path checks it.
func loadBinSnapshot(jsonPath string) (*Index, bool) {
	st, err := os.Stat(jsonPath)
	if err != nil {
		return nil, false // no canonical JSON → nothing to be a snapshot of
	}
	snap, ok := readBinSnapshot(jsonPath)
	if !ok {
		return nil, false
	}
	h := snap.H
	if h.Store != "" && h.Store != "json" {
		return nil, false
	}
	if h.JSONSize != st.Size() || h.JSONModNS != st.ModTime().UnixNano() {
		return nil, false // index.json rewritten since the snapshot was taken
	}
	if snap.I == nil {
		return nil, false
	}
	return snap.I, true
}

// loadSQLiteBinSnapshot returns the snapshot for a SQLite-primary store when
// its header token matches the generation persisted in the store. The
// generation token is authoritative; the database file's size/mtime are NOT
// compared, because SQLite checkpointing/close can touch them after the
// snapshot is written (and coarse mtimes made them fragile anyway). A store
// rewrite without a snapshot rewrite changes the token, so a stale snapshot
// cannot be served.
func loadSQLiteBinSnapshot(root string) (*Index, bool) {
	if !SQLiteEnabled() {
		return nil, false
	}
	if _, err := os.Stat(SQLitePath(root)); err != nil {
		return nil, false
	}
	snap, ok := readBinSnapshot(StorePath(root))
	if !ok {
		return nil, false
	}
	h := snap.H
	if h.Store != "sqlite" || h.IndexVersion != indexVersion {
		return nil, false
	}
	if snap.I == nil || !SQLiteSnapshotValid(root, h.Token, snap.I.UpdatedAt) {
		return nil, false
	}
	return snap.I, true
}

// readBinSnapshot decodes the snapshot file and validates the wrapper
// (magic/version). Store-specific freshness is the caller's job.
func readBinSnapshot(jsonPath string) (binSnapshot, bool) {
	snapPath := binSnapshotPathFor(jsonPath)
	fi, err := os.Stat(snapPath)
	if err != nil {
		return binSnapshot{}, false
	}
	if fi.Size() > snapshotMaxSize {
		// Over-cap: never read an unbounded file into memory. Degrades to
		// the JSON path like any other unreadable snapshot (see invariants).
		return binSnapshot{}, false
	}
	data, err := os.ReadFile(snapPath)
	if err != nil {
		return binSnapshot{}, false
	}
	var snap binSnapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&snap); err != nil {
		return binSnapshot{}, false
	}
	if snap.H.Magic != binSnapshotMagic || snap.H.Version != binSnapshotVersion {
		return binSnapshot{}, false
	}
	return snap, true
}
