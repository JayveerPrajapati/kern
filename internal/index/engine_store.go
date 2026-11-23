package index

import (
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/clip"
	"github.com/JayveerPrajapati/kern/internal/ignore"
	"github.com/JayveerPrajapati/kern/internal/indexdb"
	"github.com/JayveerPrajapati/kern/internal/indexmem"
	"github.com/JayveerPrajapati/kern/internal/indexwatch"
	"github.com/JayveerPrajapati/kern/internal/metrics"
)

// StorePath returns the on-disk location for the index of root.
// The index lives per-project under <root>/.kern/ so it is portable,
// self-contained, and never pollutes a global cache.
func StorePath(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return filepath.Join(abs, ".kern", "index.json")
}

// Save persists the index. The write is atomic (temp file + rename) so a
// concurrent reader never observes a partially-written index. The temp file
// is uniquely named (not a fixed .tmp path) so concurrent writers (watch
// daemon + CLI) don't race on the same temp file and corrupt the index.
// Save additionally refuses to overwrite an on-disk index whose schema
// version is NEWER than this index's: a long-lived daemon or watcher started
// with an older binary holds its own in-memory index and would otherwise
// silently clobber a newer index.json written by a current binary — the
// indexVersion guard only runs at load time, not at save time.
func (ix *Index) Save() error {
	if ix == nil || ix.skipPersist {
		return nil
	}
	// Ensure the local repository ignores .kern via .git/info/exclude without
	// dirtying or requiring a tracked .gitignore file.
	ensureGitExclude(ix.Root)

	// SQLite-primary: the WAL store is the canonical write path, so a build
	// writes one format, not three. The JSON cache below is the nosqlite
	// fallback and the read-migration path for older stores.
	if SQLiteEnabled() {
		if ix.delta != nil {
			if derr := SaveSQLiteDelta(ix.Root, ix, ix.delta); derr == nil {
				ix.delta = nil
				ix.persistAdjacency()
				if werr := writeSQLiteBinSnapshot(ix, ix.Root); werr != nil {
					log.Printf("kern index: sqlite fast snapshot write failed for %s (index store is authoritative): %v", clip.ClipReflect(ix.Root, 160), werr)
				}
				return nil
			} else {
				log.Printf("kern index: delta persist failed for %s, falling back to a full save: %v", clip.ClipReflect(ix.Root, 160), derr)
			}
		}
		if serr := SaveSQLite(ix.Root, ix); serr == nil {
			ix.persistAdjacency()
			// Derived fast snapshot: the SQLite store remains canonical, and
			// the snapshot header carries the store's generation token so a
			// stale snapshot can never win. Best-effort — a snapshot failure
			// only costs the next process a full SQLite load.
			if werr := writeSQLiteBinSnapshot(ix, ix.Root); werr != nil {
				log.Printf("kern index: sqlite fast snapshot write failed for %s (index store is authoritative): %v", clip.ClipReflect(ix.Root, 160), werr)
			}
			return nil
		} else {
			// LOW-8: the root is operator-supplied — clip before reflecting.
			log.Printf("kern index: sqlite persist failed for %s, falling back to the JSON cache: %v", clip.ClipReflect(ix.Root, 160), serr)
		}
	}
	data, err := json.Marshal(ix)
	if err != nil {
		return err
	}
	p := StorePath(ix.Root)
	if v := onDiskVersion(p); v > ix.Version {
		return fmt.Errorf("refusing to overwrite index schema v%d with v%d: the on-disk index was written by a newer kern — restart this process so it loads the newer index", v, ix.Version)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}

	// Unique temp file avoids the race where two processes both write to
	// p + ".tmp" and one truncates the other's bytes before rename.
	f, err := os.CreateTemp(filepath.Dir(p), ".kern-index-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := f.Name()
	defer func() { _ = os.Remove(tmpPath) }() // no-op if rename succeeded
	if _, err := f.Write(data); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Sync(); err != nil {
		_ = f.Close()
		return err
	}
	if err := f.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, p); err != nil {
		return err
	}
	// Binary snapshot cache (Rec P0-3): written alongside the canonical JSON
	// so the next Load/LoadFile can skip the multi-MB JSON parse. Best
	// effort — a snapshot failure is non-fatal (JSON remains canonical).
	_ = writeBinSnapshot(ix, p)
	return nil
}

// PruneLegacyJSON removes the frozen index.json cache once a SQLite store
// exists (the canonical store; JSON is only the nosqlite/legacy-migration
// format). Keeps the derived .snap, which is the live SQLite fast snapshot.
func PruneLegacyJSON(root string) bool {
	if !SQLiteEnabled() {
		return false
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return indexdb.PruneFrozenJSON(SQLitePath(abs), StorePath(abs))
}

// LegacyJSONSize returns the size of a frozen index.json beside a SQLite
// store (0 when absent), for kern doctor to surface retention work.
func LegacyJSONSize(root string) int64 {
	if !SQLiteEnabled() {
		return 0
	}
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	return indexdb.FrozenJSONSize(SQLitePath(abs), StorePath(abs))
}

// onDiskVersion reads the "version" field of a persisted index without
// decoding the whole (potentially multi-MB) file. The field appears as the
// second member of the object (struct field order: Root, Version, ...), so
// scanning the first 4KB is deterministic for kern-written files. Returns 0
// when the file is absent, unreadable, or the field cannot be found — the
// Save guard then no-ops, preserving current behavior.
func onDiskVersion(p string) int {
	f, err := os.Open(p)
	if err != nil {
		return 0
	}
	defer func() { _ = f.Close() }()
	buf := make([]byte, 4096)
	n, _ := io.ReadFull(f, buf)
	if n == 0 {
		return 0
	}
	m := versionFieldRe.FindSubmatch(buf[:n])
	if m == nil {
		return 0
	}
	v, err := strconv.Atoi(string(m[1]))
	if err != nil {
		return 0
	}
	return v
}

// versionFieldRe matches the index's schema version member. A literal
// `"version":` cannot occur inside the Root string (paths cannot contain
// quotes), so the first match is the real field.
var versionFieldRe = regexp.MustCompile(`"version"\s*:\s*(\d+)`)

// ensureGitExclude guarantees that <root>/.git/info/exclude includes .kern/
// so that whenever kern indexes any repository, git never tracks or shows
// .kern in git status, without creating any git diffs in client/shared repos.
func ensureGitExclude(root string) {
	if root == "" {
		return
	}
	gitDir := filepath.Join(root, ".git")
	fi, err := os.Stat(gitDir)
	if err != nil {
		return
	}
	var infoDir string
	if fi.IsDir() {
		infoDir = filepath.Join(gitDir, "info")
	} else if b, err := os.ReadFile(gitDir); err == nil {
		line := strings.TrimSpace(string(b))
		if strings.HasPrefix(line, "gitdir:") {
			target := strings.TrimSpace(strings.TrimPrefix(line, "gitdir:"))
			if !filepath.IsAbs(target) {
				target = filepath.Join(root, target)
			}
			infoDir = filepath.Join(target, "info")
		} else {
			return
		}
	} else {
		return
	}
	_ = os.MkdirAll(infoDir, 0o700)
	excludePath := filepath.Join(infoDir, "exclude")
	b, _ := os.ReadFile(excludePath)
	content := string(b)
	for _, line := range strings.Split(content, "\n") {
		if trimmed := strings.TrimSpace(line); trimmed == ".kern" || trimmed == ".kern/" {
			return // already excluded
		}
	}
	separator := "\n"
	if len(content) == 0 || strings.HasSuffix(content, "\n") {
		separator = ""
	}
	if err := os.WriteFile(excludePath, []byte(content+separator+"# kern local exclude\n.kern/\n"), 0o644); err != nil {
		// Benign for indexing, but never invisible: without the entry git
		// tracks .kern/, so say why it is missing.
		log.Printf("kern index: could not add .kern/ to %s (git may show .kern as untracked): %v", excludePath, err)
	}
}

// Load reads the index for root. Returns nil if absent.
// MemoizeLoad enables per-process memoization of hydrated indexes. Set it only
// in a warm, read-only process (cmd/kern's daemon); one-shot CLI processes must
// always read the store. Correctness rests on the size+mtime generation of the
// SQLite and JSON stores (both written via temp-file + rename), so any writer
// invalidates the memo on the next load.
var MemoizeLoad bool

func Load(root string) (*Index, error) {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	if MemoizeLoad {
		if ix, ok := indexmem.Cached[*Index](abs, []string{SQLitePath(abs), StorePath(abs)}); ok {
			return ix, nil
		}
	}
	ix, err := loadStore(abs)
	if MemoizeLoad && err == nil {
		indexmem.Put(abs, []string{SQLitePath(abs), StorePath(abs)}, ix)
	}
	return ix, err
}

// loadStore hydrates the index for an absolute root from disk (derived
// snapshot, then bin snapshot, then SQLite, then the JSON document). It is the
// body of Load without the warm-process memo layer (see memcache.go).
func loadStore(abs string) (*Index, error) {
	p := StorePath(abs)
	// Operator escape hatch: KERN_INDEX_NO_SNAPSHOT=1 bypasses both derived
	// snapshot caches and loads the canonical store, for diagnosing a suspect
	// snapshot without deleting files.
	useSnapshot := os.Getenv("KERN_INDEX_NO_SNAPSHOT") != "1"
	// Fastest path: the derived snapshot of the SQLite-primary store, trusted
	// only when its header token matches the store's persisted generation.
	// This is the restored fast load (the audit found Save stopped writing the
	// snapshot, so every CLI process paid the full SQLite materialization).
	if useSnapshot {
		if ix, ok := loadSQLiteBinSnapshot(abs); ok {
			reRootIndex(ix, abs)
			ix.initMaps()
			// SymbolsByFile is excluded from the snapshot (it duplicated every
			// symbol); rebuild it from Symbols, exactly as the JSON path does.
			ix.buildSymbolsByFile()
			metrics.Default().RecordCacheHit()
			return ix, nil
		}
	}
	// Fast path (Rec P0-3): a fresh binary snapshot decodes ~5-10x faster
	// than the JSON document; JSON stays canonical and any miss/staleness
	// falls through to the existing path below. The snapshot is trusted only
	// while the JSON it mirrors is not out-dated by a newer SQLite store:
	// since Save became SQLite-primary, index.json is no longer rewritten, so
	// a legacy snapshot would otherwise keep matching its frozen index.json
	// stat and serve stale content forever.
	if useSnapshot && !sqliteStoreNewerThanJSON(p) {
		if ix, ok := loadBinSnapshot(p); ok {
			if ix.Version != indexVersion {
				metrics.Default().RecordCacheMiss()
				return nil, fmt.Errorf("index version %d (want %d): rebuild required", ix.Version, indexVersion)
			}
			reRootIndex(ix, abs)
			ix.initMaps()
			// The lookup caches (symbolIdx, kindIdx) are deferred to the
			// first query that needs them; SymbolsByFile is the only map
			// built here because it is an exported field consumers read
			// directly after Load.
			ix.buildSymbolsByFile()
			metrics.Default().RecordCacheHit()
			return ix, nil
		}
	}
	// SQLite-primary store (default build): read it when present. Load errors
	// are tolerated (mirroring project.Session's load chain) — a corrupt or
	// version-mismatched store falls through to the JSON migration path, and
	// OpenSQLite self-heals a corrupt file by quarantining it.
	if SQLiteEnabled() {
		if _, serr := os.Stat(SQLitePath(abs)); serr == nil {
			if ix, lerr := LoadSQLite(abs); lerr == nil && ix != nil {
				reRootIndex(ix, abs)
				metrics.Default().RecordCacheHit()
				return ix, nil
			}
		}
	}
	data, err := os.ReadFile(p)
	if err != nil {
		metrics.Default().RecordCacheMiss()
		return nil, err
	}
	ix := &Index{}
	if err := json.Unmarshal(data, ix); err != nil {
		metrics.Default().RecordCacheMiss()
		return nil, err
	}
	if ix.Version != indexVersion {
		metrics.Default().RecordCacheMiss()
		return nil, fmt.Errorf("index version %d (want %d): rebuild required", ix.Version, indexVersion)
	}
	reRootIndex(ix, abs)
	ix.initMaps()
	// The lookup caches (symbolIdx, kindIdx) are deferred to the first
	// query that needs them; SymbolsByFile is the only map built here
	// because it is an exported field consumers read directly after Load.
	ix.buildSymbolsByFile()
	metrics.Default().RecordCacheHit()
	return ix, nil
}

// reRootIndex re-points a loaded index's Root at the directory it was loaded
// for when the recorded root is a DIFFERENT absolute path. Without this, Stale() → FreshnessProof(ix.Root) would
// evaluate the original tree — which is unchanged — and report the index
// "fresh" forever, so LoadOrBuild / `kern index` silently reuse a stale index
// and only `kern index --force` healed it. Re-pointing makes every subsequent
// freshness evaluation compare the recorded identity against THIS tree, so a
// moved repo rebuilds automatically on the next load (and a byte-identical
// copy — same git tree OID — stays fresh but with paths resolving into the
// copy, which the write-path out-of-root guards already cover). Relative
// recorded roots (the normal `kern index .` case) are left untouched: they
// resolve to the same directory and forcing a rebuild on every load would be
// a needless full re-walk.
func reRootIndex(ix *Index, abs string) {
	if ix == nil {
		return
	}
	if ix.Root != "" && filepath.IsAbs(ix.Root) && filepath.Clean(ix.Root) != filepath.Clean(abs) {
		ix.Root = abs
	}
	// Seed the stat-gate with the previous process's content walk. Harmless
	// for git roots (the git OID path never consults it) and load-bearing for
	// non-git exports, where it turns a per-process re-hash into stats.
	ix.loadPersistedFreshness()
}

// sqliteStoreNewerThanJSON reports whether the SQLite store was written after
// the JSON cache at jsonPath. It guards the gob-snapshot fast path in Load:
// the snapshot's freshness proof compares against index.json's stat, and once
// Save is SQLite-primary (default build) index.json is no longer rewritten, so
// a legacy snapshot would otherwise keep matching its frozen stat and serve
// stale content forever. When SQLite is compiled out, or either file is
// missing, it reports false and the snapshot path is left to its own
// (JSON-stat) freshness check.
func sqliteStoreNewerThanJSON(jsonPath string) bool {
	if !SQLiteEnabled() {
		return false
	}
	jj, err := os.Stat(jsonPath)
	if err != nil {
		return false // no JSON → nothing for a snapshot to mirror
	}
	sj, err := os.Stat(filepath.Join(filepath.Dir(jsonPath), "index.sqlite"))
	if err != nil {
		return false // no SQLite store → legacy JSON/snapshot path stands
	}
	return sj.ModTime().After(jj.ModTime())
}

// Stale reports whether a source file was added, removed, or edited since the
// index was built, so intel never serves out-of-date call graphs. The
// authoritative verdict comes from FreshnessProof (git tree OID, falling back
// to a content re-hash).
//
// An earlier version kept a stat-only count gate here (walked-file count vs
// len(FileHashes)) as a cheap pre-rejection. It was removed: the two counts
// are not comparable — the build walk admits quickExt files that never enter
// FileHashes (e.g. LICENSE, Makefile, content-unindexable .md), so on any
// repo containing such files the gate reported stale PERMANENTLY, forcing
// every Stale() caller (guard check, LoadOrBuild, web console, Session) down
// a needless full update path and defeating web's staleCooldown. A genuine
// count change (file added/removed) always changes the content root too, so
// the proof alone decides — the gate saved latency only on indexes that were
// stale anyway, and every caller rebuilds immediately after a stale verdict.
func (ix *Index) Stale() bool {
	if ix == nil || len(ix.FileHashes) == 0 {
		if ix != nil {
			// No baseline to compare: no content walk ran, so no observation
			// is retained for a following update.
			ix.clearStaleSnapshot()
		}
		return true
	}
	// No recorded identity (in-memory test index, or hand-built Index struct):
	// fall back to the pre-identity content-hash comparison.
	if ix.Identity == nil {
		return ix.legacyStale()
	}
	return ix.FreshnessProof(ix.Root).Stale()
}

// legacyStale is the pre-identity staleness check: the mtime fast gate plus
// an exact content-hash walk. Retained for indexes with a nil Identity (e.g.
// in-memory test indexes constructed without a Build) where there is no
// FreshnessProof baseline to compare against. Note the gate here returns
// "not stale" on a match, so mtime-preserving edits evade it — acceptable for
// the defensive path only, never for persisted indexes.
func (ix *Index) legacyStale() bool {
	ign := ignore.Load(ix.Root)
	// Fast gate: identical file count and newest mtime mean nothing changed, so
	// skip re-hashing. An mtime-preserving edit (rare) evades the gate.
	if ix.MaxMtime > 0 {
		if maxMtime, count, err := indexwatch.IndexableMaxMtime(ix.Root, ign); err == nil && count == len(ix.FileHashes) && maxMtime == ix.MaxMtime {
			ix.clearStaleSnapshot() // no content walk ran
			return false
		}
	}
	obs, err := indexwatch.IndexableHashesObserved(ix.Root, ign, proofWalkMaxBytes())
	if err != nil {
		ix.clearStaleSnapshot() // no usable observation
		return true
	}
	fresh := len(obs.Hashes) == len(ix.FileHashes)
	if fresh {
		for f, h := range obs.Hashes {
			if ph, ok := ix.FileHashes[f]; !ok || ph != h {
				fresh = false
				break
			}
		}
	}
	ix.retainStaleSnapshot(obs, fresh)
	return !fresh
}

// LoadFile reads an index directly from a store path (used for
// cross-project search across the cache directory).
func LoadFile(path string) (*Index, error) {
	// Fast path (Rec P0-3): same snapshot preference as Load; JSON canonical.
	if ix, ok := loadBinSnapshot(path); ok {
		if ix.Version != indexVersion {
			return nil, fmt.Errorf("index version %d (want %d): rebuild required", ix.Version, indexVersion)
		}
		ix.initMaps()
		// SymbolsByFile is built eagerly (exported field, read directly by
		// consumers); the symbolIdx/kindIdx lookup caches are deferred to
		// the first query.
		ix.buildSymbolsByFile()
		return ix, nil
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	ix := &Index{}
	if err := json.Unmarshal(data, ix); err != nil {
		return nil, err
	}
	if ix.Version != indexVersion {
		return nil, fmt.Errorf("index version %d (want %d): rebuild required", ix.Version, indexVersion)
	}
	ix.initMaps()
	// SymbolsByFile is built eagerly (exported field, read directly by
	// consumers); the symbolIdx/kindIdx lookup caches are deferred to the
	// first query.
	ix.buildSymbolsByFile()
	return ix, nil
}
