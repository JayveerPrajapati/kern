package agentwire

import (
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/JayveerPrajapati/kern/internal/rulesblock"
)

// RuleFileMode returns the permission bits for a rule file: existing files
// keep their current bits, new files default to 0644.
func RuleFileMode(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0o644
}

// BackupFile copies path to path+".bak.<timestamp>" before a rewrite so a
// destructive edit never destroys the user's original content without recourse.
// After the copy it prunes this file's older backups down to the 3 most
// recent (pruneOldBackups), so repeated setup runs cannot litter config dirs
// with unbounded .bak.<timestamp> copies.
func BackupFile(path string) error {
	b, err := os.ReadFile(path)
	if err != nil || len(b) == 0 {
		return nil
	}
	ts := time.Now().Format("20060102-150405")
	mode := os.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	if err := os.WriteFile(path+".bak."+ts, b, mode); err != nil {
		return err
	}
	// Best-effort hygiene: pruning failure must not fail the backup — the
	// backup copy is the safety-critical part, cleanup is not.
	_ = pruneOldBackups(path)
	return nil
}

// pruneOldBackups deletes backup siblings of path (path+".bak.<timestamp>")
// beyond the 3 most recent, so a managed file never accumulates more than 3
// backups. The .bak.<timestamp> suffix sorts lexicographically (fixed-width
// YYYYMMDD-HHMMSS), so "most recent" is simply the 3 highest names.
func pruneOldBackups(path string) error {
	base := filepath.Base(path)
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return err
	}
	var baks []string
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.HasPrefix(e.Name(), base+".bak.") {
			baks = append(baks, e.Name())
		}
	}
	if len(baks) <= 3 {
		return nil
	}
	sort.Sort(sort.Reverse(sort.StringSlice(baks)))
	for _, name := range baks[3:] {
		if err := os.Remove(filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return nil
}

// RemoveKernSection removes EVERY kern-managed block from s — both the
// marker-delimited format and the unmarked "# kern usage rules" format —
// preserving all other content. The pure text manipulation lives in
// internal/rulesblock; this wrapper keeps the setup-internal signature.
func RemoveKernSection(s string) (string, error) {
	return rulesblock.ExciseKernBlock(s)
}

// MergeAppend removes any existing kern section from existing and appends the
// fresh kern block at the end, preserving all other content.
func MergeAppend(existing, kern string) (string, error) {
	cleaned, err := RemoveKernSection(existing)
	if err != nil {
		return "", err
	}
	cleaned = strings.TrimRight(cleaned, "\n")
	kern = strings.TrimRight(kern, "\n")
	if cleaned == "" {
		return kern + "\n", nil
	}
	return cleaned + "\n\n" + kern + "\n", nil
}
