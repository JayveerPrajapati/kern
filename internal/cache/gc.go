package cache

// TTL-based eviction of stale cache
// files + gzip archival of dormant ones — the "active in RAM, dormant
// archived to disk" lifecycle. Dormant entries older than
// archiveAfter are compressed to "<name>.json.gz" (readers transparently
// fall back to the twin, see Load), and anything older than evictAfter is
// deleted outright. Everything is best-effort and never disturbs callers.

import (
	"compress/gzip"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/config"
)

// minArchiveBytes is the floor below which a plain .json file is not worth
// gzipping (tiny files cost more in inodes than they save in bytes).
const minArchiveBytes = 4 << 10 // 4 KiB

// maintainInterval is how often MaintainOnce re-runs the GC pass per
// directory (rate limit via the .maintained-at marker).
const maintainInterval = time.Hour

// maintainMarker is the rate-limit marker file MaintainOnce writes into a
// directory; its content is the unix timestamp of the last run.
const maintainMarker = ".maintained-at"

// Maintain walks dir recursively (including subdirectories such as
// data/sem/**, where semcache payloads and index files live) and applies the
// lifecycle to every *.json / *.json.gz file it finds:
//
//   - mtime older than evictAfter → the file is deleted (and its .gz twin if
//     present) and counted as evicted;
//   - otherwise a plain .json older than archiveAfter and larger than
//     minArchiveBytes → gzipped to "<name>.json.gz" (temp + rename, original
//     mtime preserved so dormancy tracking stays correct) and the plain file
//     removed; counted as archived;
//   - everything else is left alone. *.json.gz files are never re-archived
//     and non-.json files are skipped.
//
// A duration <= 0 disables its pass (archiveAfter <= 0 → no archiving;
// evictAfter <= 0 → no eviction). With dryRun the same decisions are made and
// counted but nothing on disk is modified.
func Maintain(dir string, archiveAfter, evictAfter time.Duration, dryRun bool) (archived, evicted int, err error) {
	now := time.Now()
	walkErr := filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			// Only the ROOT dir error aborts the walk (missing/unreadable
			// root is the documented error contract). A child that errors
			// mid-walk (e.g. a permission-denied subdirectory) is skipped
			// best-effort so one bad directory cannot starve GC of the
			// whole tree (gate-2 attempt-1).
			if path == dir {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil // recurse; only files are candidates
		}
		name := d.Name()
		isGz := strings.HasSuffix(name, ".json.gz")
		if !isGz && !strings.HasSuffix(name, ".json") {
			return nil // only cache entries (and their twins)
		}
		info, err := d.Info()
		if err != nil {
			return nil // vanished mid-walk; best-effort
		}
		age := now.Sub(info.ModTime())
		if evictAfter > 0 && age > evictAfter {
			if !dryRun {
				_ = os.Remove(path)
				if !isGz {
					_ = os.Remove(path + ".gz")
				}
			}
			evicted++
			return nil
		}
		if isGz {
			return nil // already archived; never re-archive (G-7)
		}
		if archiveAfter <= 0 || age <= archiveAfter {
			return nil // too fresh, or archiving disabled
		}
		if info.Size() < minArchiveBytes {
			return nil // too small to bother gzipping
		}
		if !dryRun {
			if err := gzipFile(path); err != nil {
				return nil // best-effort: skip files we could not compress
			}
		}
		archived++
		return nil
	})
	return archived, evicted, walkErr
}

// MaintainDefaults runs Maintain on dir with the durations from
// KERN_CACHE_ARCHIVE_DAYS (default 7) and KERN_CACHE_TTL_DAYS (default 30,
// the eviction age) — or cache.archive_days / cache.ttl_days in
// .kern/config.json — parsed as days (float ok). A value <= 0 disables that
// pass; unknown/garbage values fall back to the default. With dryRun the pass
// counts without mutating. It then runs TrimToBudget with the size ceiling
// from KERN_CACHE_MAX_MB (default 1024) / cache.max_mb, so the cache dir
// cannot grow unboundedly within the TTL window (Persona 8/16: 154.6 MiB
// observed with 0 TTL evictions — age alone never bounded disk).
func MaintainDefaults(dir string, dryRun bool) (archived, evicted, trimmed int, err error) {
	archiveAfter := daysFromConfig("KERN_CACHE_ARCHIVE_DAYS", "cache.archive_days", 7)
	evictAfter := daysFromConfig("KERN_CACHE_TTL_DAYS", "cache.ttl_days", 30)
	archived, evicted, err = Maintain(dir, archiveAfter, evictAfter, dryRun)
	if err != nil {
		return archived, evicted, 0, err
	}
	trimmed, terr := TrimToBudget(dir, MaxBudgetBytes(), dryRun)
	if terr != nil {
		// The TTL pass already succeeded; a trim failure must not fail the
		// whole maintain (best-effort contract).
		return archived, evicted, 0, nil
	}
	return archived, evicted, trimmed, err
}

// MaxBudgetBytes returns the cache size ceiling in bytes from
// KERN_CACHE_MAX_MB / cache.max_mb (default 1024 MiB). A value <= 0
// disables the budget (TrimToBudget becomes a no-op).
func MaxBudgetBytes() int64 {
	mb := config.Float64("", "KERN_CACHE_MAX_MB", "cache.max_mb", 1024)
	if mb <= 0 {
		return 0
	}
	return int64(mb * 1024 * 1024)
}

// trimCandidate is one cache file considered by the size-budget trim pass.
type trimCandidate struct {
	path  string
	size  int64
	mtime time.Time
	isGz  bool
}

// TrimToBudget enforces a size ceiling on dir: when the total size of all
// *.json / *.json.gz cache files exceeds maxBytes, the oldest-mtime entries
// are deleted (with their gzip twin when present) until the total is within
// the budget. maxBytes <= 0 disables the pass. With dryRun the same
// decisions are counted but nothing is deleted. Deletion is oldest-first so
// the budget protects the disk while keeping the freshest entries hot.
func TrimToBudget(dir string, maxBytes int64, dryRun bool) (int, error) {
	if maxBytes <= 0 {
		return 0, nil
	}
	var candidates []trimCandidate
	var total int64
	walkErr := filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			if path == dir {
				return err
			}
			if d != nil && d.IsDir() {
				return fs.SkipDir
			}
			return nil
		}
		if d.IsDir() {
			return nil
		}
		name := d.Name()
		isGz := strings.HasSuffix(name, ".json.gz")
		if !isGz && !strings.HasSuffix(name, ".json") {
			return nil
		}
		info, err := d.Info()
		if err != nil {
			return nil // vanished mid-walk; best-effort
		}
		candidates = append(candidates, trimCandidate{path: path, size: info.Size(), mtime: info.ModTime(), isGz: isGz})
		total += info.Size()
		return nil
	})
	if walkErr != nil {
		return 0, walkErr
	}
	if total <= maxBytes {
		return 0, nil
	}
	sort.SliceStable(candidates, func(i, j int) bool { return candidates[i].mtime.Before(candidates[j].mtime) })
	trimmed := 0
	for _, c := range candidates {
		if total <= maxBytes {
			break
		}
		if !dryRun {
			_ = os.Remove(c.path)
			if !c.isGz {
				_ = os.Remove(c.path + ".gz")
			}
		}
		total -= c.size
		trimmed++
	}
	return trimmed, nil
}

// MaintainOnce is the opportunistic, rate-limited driver. It checks the
// <dir>/.maintained-at marker and, if it is missing or older than an hour,
// writes a fresh marker and runs MaintainDefaults. All errors are swallowed:
// the pass is best-effort and must never disturb Store/Load callers. The
// marker write is best-effort too, so concurrent processes racing here are
// harmless — the worst case is two overlapping passes.
func MaintainOnce(dir string) {
	if !maintainDue(dir) {
		return
	}
	_ = writeMaintainMarker(dir)
	_, _, _, _ = MaintainDefaults(dir, false)
}

// maintainDue reports whether the <dir>/.maintained-at marker is missing or
// older than maintainInterval. A corrupt marker counts as due (it will be
// rewritten).
func maintainDue(dir string) bool {
	data, err := os.ReadFile(filepath.Join(dir, maintainMarker))
	if err != nil {
		return true // missing or unreadable → due
	}
	ts, err := strconv.ParseInt(strings.TrimSpace(string(data)), 10, 64)
	if err != nil {
		return true // corrupt marker → due
	}
	return time.Since(time.Unix(ts, 0)) > maintainInterval
}

// writeMaintainMarker stamps <dir>/.maintained-at with the current unix time,
// creating the directory if needed.
func writeMaintainMarker(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	marker := filepath.Join(dir, maintainMarker)
	return os.WriteFile(marker, []byte(strconv.FormatInt(time.Now().Unix(), 10)), 0o600)
}

// gzipFile atomically replaces the plain JSON file at path with a gzip twin
// at path+".gz": it writes the compressed stream to a temp file, preserves
// the original mtime on it, renames it into place, then removes the plain
// file. A crash between the rename and the remove leaves both variants — a
// benign state (Load prefers the plain file; the next Store drops the twin).
func gzipFile(path string) error {
	in, err := os.Open(path)
	if err != nil {
		return err
	}
	defer func() { _ = in.Close() }()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-gz-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer func() { _ = os.Remove(tmpName) }()
	zw := gzip.NewWriter(tmp)
	if _, err := io.Copy(zw, in); err != nil {
		_ = zw.Close()
		_ = tmp.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Chtimes(tmpName, info.ModTime(), info.ModTime()); err != nil {
		return err
	}
	if err := os.Rename(tmpName, path+".gz"); err != nil {
		return err
	}
	return os.Remove(path)
}

// daysFromConfig parses a days-as-float config value (env var or
// .kern/config.json key) into a duration. Unset or garbage values fall back
// to def; a parsed value <= 0 disables the pass (returns 0). KERN_CACHE_*
// knobs.
func daysFromConfig(envName, key string, def float64) time.Duration {
	v := config.Float64("", envName, key, def)
	if v <= 0 {
		return 0 // explicit disable
	}
	return durationFromDays(v)
}

func durationFromDays(d float64) time.Duration {
	return time.Duration(d * float64(24*time.Hour))
}
