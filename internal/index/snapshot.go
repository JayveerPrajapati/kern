package index

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"
)

// SnapshotSchemaVersion is the version of the GraphSnapshot on-disk format.
// It is monotonic: bumping it invalidates every older snapshot (LoadSnapshot
// fails loudly on a mismatch instead of silently misreading the file), so
// consumers can never confuse two incompatible snapshot layouts.
const SnapshotSchemaVersion = 1

// GraphSnapshot is the canonical, self-contained graph snapshot for
// multi-agent handoff: the graph itself (whole-repo or per-symbol subgraph),
// the build-time IndexIdentity fingerprint (content root + git tree/commit),
// and the per-file SHA-256 map the snapshot was built from. A receiving agent
// can verify the snapshot against a root via VerifySnapshot without any other
// kern state — everything needed for the check travels inside the file.
type GraphSnapshot struct {
	SchemaVersion int               `json:"schema_version"`
	Mode          string            `json:"mode"` // "whole" | "subgraph"
	Root          string            `json:"root"`
	GeneratedAt   time.Time         `json:"generated_at"`
	Identity      IndexIdentity     `json:"identity"`
	Graph         GraphResult       `json:"graph"`
	Files         map[string]string `json:"files"` // path -> sha256 (copy of FileHashes)
}

// Snapshot renders the index as a canonical GraphSnapshot. Mode "whole"
// captures the whole-repo graph capped at limit (limit <= 0 selects the
// 400-symbol default); mode "subgraph" captures the neighbourhood of symbol,
// erroring when the symbol is not in the index (mirroring how callers of
// Neighborhood treat a not-found result). Files is a fresh copy of the
// index's FileHashes, so later index mutations cannot leak into the snapshot;
// Identity is the build-time identity, or the zero value when the index was
// built without one (callers then see ContentRoot "").
func (ix *Index) Snapshot(mode, symbol string, limit int) (GraphSnapshot, error) {
	snap := GraphSnapshot{
		SchemaVersion: SnapshotSchemaVersion,
		Mode:          mode,
		Root:          ix.Root,
		GeneratedAt:   time.Now(),
		Files:         map[string]string{},
	}
	if ix.Identity != nil {
		snap.Identity = *ix.Identity
	}
	switch mode {
	case "whole":
		snap.Graph = ix.WholeGraph(limit)
	case "subgraph":
		g, ok := ix.Neighborhood(symbol)
		if !ok {
			return GraphSnapshot{}, fmt.Errorf("snapshot: symbol %q not found in index", symbol)
		}
		snap.Graph = g
	default:
		return GraphSnapshot{}, fmt.Errorf("snapshot: unsupported mode %q (want \"whole\" or \"subgraph\")", mode)
	}
	for p, h := range ix.FileHashes {
		snap.Files[p] = h
	}
	return snap, nil
}

// Save writes the snapshot as indented JSON with 0644 permissions. The
// format is versioned by SnapshotSchemaVersion, so a snapshot written by a
// future kern that bumps the schema cannot be silently misread.
func (s *GraphSnapshot) Save(path string) error {
	b, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

// LoadSnapshot reads a GraphSnapshot from path and validates its schema
// version. A version mismatch is a hard error (fail loud — kern rule): an
// unrecognized layout must never be interpreted as the current one.
func LoadSnapshot(path string) (*GraphSnapshot, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var snap GraphSnapshot
	if err := json.Unmarshal(b, &snap); err != nil {
		return nil, err
	}
	if snap.SchemaVersion != SnapshotSchemaVersion {
		return nil, fmt.Errorf("snapshot schema version %d does not match supported version %d", snap.SchemaVersion, SnapshotSchemaVersion)
	}
	return &snap, nil
}

// snapshotSizeCap is the largest on-disk file VerifySnapshot will re-hash. A
// file larger than this counts as a mismatch (stale) instead of being read
// into memory — snapshots are meant for fast cross-agent handoff, not full
// re-indexing.
const snapshotSizeCap = 2 << 20 // 2 MiB

// VerifySnapshot checks whether the tree at root still matches what snap was
// built from. Non-strict mode takes the cheap git fast path (working-tree
// tree OID compare against snap.Identity.TreeOID) and only falls back to a
// full per-file SHA-256 walk when git cannot vouch for the tree; strict mode
// always walks. A nil snapshot or a schema-version mismatch yields
// FreshnessUnknown with no error. Verdicts:
//   - fresh:  every recorded file still hashes to its recorded value (or git
//     vouches for the tree).
//   - stale:  any recorded file is missing, unreadable, larger than the cap,
//     or hashes differently.
//   - unknown: no snapshot baseline to verify against.
func VerifySnapshot(root string, snap *GraphSnapshot, strict bool) (FreshnessVerdict, error) {
	if snap == nil || snap.SchemaVersion != SnapshotSchemaVersion {
		return FreshnessUnknown, nil
	}
	// Fast path: git's working-tree OID is unchanged, so no indexed file
	// changed — done without a content re-walk.
	if !strict && snap.Identity.TreeOID != "" {
		if cur := treeOID(root); cur != "" {
			if cur == snap.Identity.TreeOID {
				return FreshnessFresh, nil
			}
			return FreshnessStale, nil
		}
		// git unavailable at root: fall through to the content walk.
	}
	paths := make([]string, 0, len(snap.Files))
	for p := range snap.Files {
		paths = append(paths, p)
	}
	sort.Strings(paths)
	for _, p := range paths {
		full := filepath.Join(root, p)
		fi, err := os.Stat(full)
		if err != nil {
			return FreshnessStale, nil // deleted or unreadable
		}
		if fi.Size() > snapshotSizeCap {
			return FreshnessStale, nil // oversized: count as mismatch
		}
		data, err := os.ReadFile(full)
		if err != nil {
			return FreshnessStale, nil
		}
		sum := sha256.Sum256(data)
		if hex.EncodeToString(sum[:]) != snap.Files[p] {
			return FreshnessStale, nil
		}
	}
	return FreshnessFresh, nil
}
