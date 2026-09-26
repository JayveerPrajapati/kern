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
//   - index.json remains the canonical persisted format of the fallback path.
//     Save is SQLite-primary in the default build, so the JSON + binary
//     snapshot pair is written only when SQLite is compiled out (-tags
//     nosqlite) or the SQLite write fails; index.json is then the read
//     migration path for caches written by older kern, and Load/LoadFile
//     fall back to it whenever the snapshot is missing, stale, or
//     undecodable.
//   - Freshness is proven by comparing the snapshot header against the
//     current index.json stat (size + nanosecond mtime): any rewrite of the
//     JSON invalidates the snapshot, so a stale snapshot can never be served.
//     Load additionally refuses the snapshot when a newer SQLite store
//     supersedes the JSON (sqliteStoreNewerThanJSON), so a legacy snapshot
//     cannot shadow the SQLite-primary store.
//   - A corrupt/foreign snapshot degrades to the JSON path, never to an
//     error: loadBinSnapshot returns ok=false on any failure (including an
//     over-cap file, which is never read into memory).
//
// The SQLite store (default build; disabled only with -tags nosqlite) remains
// the richer store; this snapshot exists specifically so the default build
// stops paying the JSON parse cost on every CLI invocation.
package index

import (
	"bytes"
	"encoding/gob"
	"os"
	"path/filepath"
)

// binSnapshotMagic identifies the file as a kern binary index snapshot;
// binSnapshotVersion is the snapshot format version (independent of
// indexVersion — bump only when the wrapper/header layout changes).
const (
	binSnapshotMagic   = "KERNSNAP"
	binSnapshotVersion = 1
)

// binSnapshotHeader records the source JSON's identity so a stale snapshot
// (written before a newer index.json) is rejected by comparison, never by
// trust. All fields exported for gob.
type binSnapshotHeader struct {
	Magic     string
	Version   int
	JSONSize  int64
	JSONModNS int64
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
	snap := binSnapshot{
		H: binSnapshotHeader{
			Magic:     binSnapshotMagic,
			Version:   binSnapshotVersion,
			JSONSize:  st.Size(),
			JSONModNS: st.ModTime().UnixNano(),
		},
		I: ix,
	}
	// Unique temp name (like Save's) so concurrent writers (watch daemon +
	// CLI) never share a temp file; rename is atomic, so a reader only ever
	// sees a complete snapshot.
	tmp, err := os.CreateTemp(filepath.Dir(jsonPath), ".index.snap.*.tmp")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }() // no-op if rename succeeded
	if err := gob.NewEncoder(tmp).Encode(&snap); err != nil {
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
	return os.Rename(tmpName, binSnapshotPathFor(jsonPath))
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
	snapPath := binSnapshotPathFor(jsonPath)
	fi, err := os.Stat(snapPath)
	if err != nil {
		return nil, false
	}
	if fi.Size() > snapshotMaxSize {
		// Over-cap: never read an unbounded file into memory. Degrades to
		// the JSON path like any other unreadable snapshot (see invariants).
		return nil, false
	}
	data, err := os.ReadFile(snapPath)
	if err != nil {
		return nil, false
	}
	var snap binSnapshot
	if err := gob.NewDecoder(bytes.NewReader(data)).Decode(&snap); err != nil {
		return nil, false
	}
	h := snap.H
	if h.Magic != binSnapshotMagic || h.Version != binSnapshotVersion {
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
