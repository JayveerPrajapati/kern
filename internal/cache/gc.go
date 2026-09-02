package cache

// G-7 (docs/audit/next-plan-gaps.md): TTL-based eviction of stale cache
// files + gzip archival of dormant ones — the "active in RAM, dormant
// archived to disk" half of report STEP 3. Dormant entries older than
// archiveAfter are compressed to "<name>.json.gz" (readers transparently
// fall back to the twin, see Load), and anything older than evictAfter is
// deleted outright. Everything is best-effort and never disturbs callers.

import (
	"compress/gzip"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
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

// Maintain walks dir (non-recursively) and applies the G-7 lifecycle to the
// top-level *.json / *.json.gz files it finds:
//
//   - mtime older than evictAfter → the file is deleted (and its .gz twin if
//     present) and counted as evicted;
//   - otherwise a plain .json older than archiveAfter and larger than
//     minArchiveBytes → gzipped to "<name>.json.gz" (temp + rename, original
//     mtime preserved so dormancy tracking stays correct) and the plain file
//     removed; counted as archived;
//   - everything else is left alone. *.json.gz files are never re-archived,
//     non-.json files are skipped, and subdirectories are never touched.
//
// A duration <= 0 disables its pass (archiveAfter <= 0 → no archiving;
// evictAfter <= 0 → no eviction). With dryRun the same decisions are made and
// counted but nothing on disk is modified.
func Maintain(dir string, archiveAfter, evictAfter time.Duration, dryRun bool) (archived, evicted int, err error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0, 0, err
	}
	now := time.Now()
	for _, e := range entries {
		if e.IsDir() {
			continue // never touch subdirectories (G-7)
		}
		name := e.Name()
		isGz := strings.HasSuffix(name, ".json.gz")
		if !isGz && !strings.HasSuffix(name, ".json") {
			continue // only cache entries (and their twins)
		}
		path := filepath.Join(dir, name)
		info, err := e.Info()
		if err != nil {
			continue // vanished mid-walk; best-effort
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
			continue
		}
		if isGz {
			continue // already archived; never re-archive (G-7)
		}
		if archiveAfter <= 0 || age <= archiveAfter {
			continue // too fresh, or archiving disabled
		}
		if info.Size() < minArchiveBytes {
			continue // too small to bother gzipping
		}
		if !dryRun {
			if err := gzipFile(path); err != nil {
				continue // best-effort: skip files we could not compress
			}
		}
		archived++
	}
	return archived, evicted, nil
}

// MaintainDefaults runs Maintain on dir with the durations from the
// KERN_CACHE_ARCHIVE_DAYS (default 7) and KERN_CACHE_TTL_DAYS (default 30,
// the eviction age) environment variables, parsed as days (float ok).
// A value <= 0 disables that pass; unknown/garbage values fall back to the
// default. With dryRun the pass counts without mutating.
func MaintainDefaults(dir string, dryRun bool) (archived, evicted int, err error) {
	archiveAfter := daysFromEnv("KERN_CACHE_ARCHIVE_DAYS", 7)
	evictAfter := daysFromEnv("KERN_CACHE_TTL_DAYS", 30)
	return Maintain(dir, archiveAfter, evictAfter, dryRun)
}

// MaintainOnce is the opportunistic, rate-limited G-7 driver. It checks the
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
	_, _, _ = MaintainDefaults(dir, false)
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
	defer in.Close()
	info, err := in.Stat()
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".tmp-gz-*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	defer os.Remove(tmpName)
	zw := gzip.NewWriter(tmp)
	if _, err := io.Copy(zw, in); err != nil {
		zw.Close()
		tmp.Close()
		return err
	}
	if err := zw.Close(); err != nil {
		tmp.Close()
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

// daysFromEnv parses a days-as-float environment variable into a duration.
// Unset or garbage values fall back to def; a parsed value <= 0 disables the
// pass (returns 0). KERN_CACHE_* knobs (G-7).
func daysFromEnv(name string, def float64) time.Duration {
	raw := os.Getenv(name)
	if raw == "" {
		return durationFromDays(def)
	}
	v, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return durationFromDays(def) // garbage → default
	}
	if v <= 0 {
		return 0 // explicit disable
	}
	return durationFromDays(v)
}

func durationFromDays(d float64) time.Duration {
	return time.Duration(d * float64(24*time.Hour))
}
