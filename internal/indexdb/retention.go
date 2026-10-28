package indexdb

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io/fs"
	"log"
	"os"
	"path/filepath"
	"time"
)

// GenerationToken derives the store generation token persisted in the SQLite
// meta table ("generation") and carried by the derived snapshot header. It
// changes whenever the index content it describes changes, independent of
// filesystem mtime granularity (coarse mtime, cp -p, rsync -t).
func GenerationToken(version int, updated time.Time, symbols, calls, hashes int, contentRoot string) string {
	h := sha256.New()
	fmt.Fprintf(h, "v%d|%s|%d|%d|%d", version, updated.UTC().Format(time.RFC3339Nano), symbols, calls, hashes)
	if contentRoot != "" {
		fmt.Fprintf(h, "|%s", contentRoot)
	}
	return hex.EncodeToString(h.Sum(nil))
}

// FrozenJSONSize returns the size of jsonPath when sqlitePath exists (a
// leftover legacy cache beside the canonical store), else 0.
func FrozenJSONSize(sqlitePath, jsonPath string) int64 {
	if _, err := os.Stat(sqlitePath); err != nil {
		return 0
	}
	st, err := os.Stat(jsonPath)
	if err != nil {
		return 0
	}
	return st.Size()
}

// PruneFrozenJSON removes a leftover legacy JSON cache when the canonical
// SQLite store exists. Best-effort; reports whether a file was removed.
func PruneFrozenJSON(sqlitePath, jsonPath string) bool {
	if _, err := os.Stat(sqlitePath); err != nil {
		return false
	}
	if _, err := os.Stat(jsonPath); err != nil {
		return false
	}
	if err := os.Remove(jsonPath); err != nil {
		log.Printf("kern index: could not prune legacy JSON cache %s: %v", jsonPath, err)
		return false
	}
	return true
}

// DirSize totals the regular files under dir (best-effort; unreadable entries
// are skipped).
func DirSize(dir string) int64 {
	var total int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if !d.IsDir() {
			if fi, eerr := d.Info(); eerr == nil {
				total += fi.Size()
			}
		}
		return nil
	})
	return total
}

// RetentionNote renders the doctor note for leftover storage that the
// retention policy will reclaim: a legacy index.json beside a SQLite store
// and/or a .kern directory past 100 MB. Empty note means nothing to report.
func RetentionNote(legacyJSONBytes, kernDirBytes int64) string {
	note := ""
	if legacyJSONBytes > 1<<20 {
		note += fmt.Sprintf("; legacy index.json %.1f MB pending prune (next `kern index` removes it)", float64(legacyJSONBytes)/(1<<20))
	}
	if kernDirBytes > 100<<20 {
		note += fmt.Sprintf("; .kern is %.0f MB (see `kern audit rotate`)", float64(kernDirBytes)/(1<<20))
	}
	return note
}
