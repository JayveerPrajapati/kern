package setupundo

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/cache"
	"github.com/JayveerPrajapati/kern/internal/setup"
)

// remove_data.go — the user-data removal half of a full uninstall. The
// wiring half (RemoveGlobal) undoes what `kern setup` wrote; this half
// removes the data kern accumulated at runtime. `kern setup --remove --data`
// and `kern uninstall` both end here.

// DataTarget is one user-data location a data removal (`kern setup --remove
// --data`, `kern uninstall`) removes. Paths are exact and resolved — they
// are never inferred by searching the filesystem for kern-shaped names.
type DataTarget struct {
	Path string
	Why  string
}

// MachineDataTargets returns every user-data directory kern owns outside the
// wiring `kern setup` writes: the ~/.kern state home (indexes, approvals,
// audit chain, memory, git hooks), the os.UserConfigDir()/kern machine
// secrets (audit-chain HMAC key, approval signing key, consumed ledger), and
// the runtime cache root. Missing locations are still listed so previews and
// inventories stay honest about everything a full uninstall owns.
func MachineDataTargets() []DataTarget {
	var targets []DataTarget
	if home := setup.GlobalHomeDir(); home != "" && home != "." {
		targets = append(targets, DataTarget{
			Path: filepath.Join(home, ".kern"),
			Why:  "kern state home (indexes, approvals, audit, memory)",
		})
	}
	if cfg, err := os.UserConfigDir(); err == nil && cfg != "" {
		targets = append(targets, DataTarget{
			Path: filepath.Join(cfg, "kern"),
			Why:  "machine secrets (audit-chain key, approval signing key)",
		})
	}
	// The cache target MUST use the same resolver the runtime writes
	// through (internal/cache.Dir: KERN_CACHE_DIR, then XDG_CACHE_HOME,
	// then ~/.cache/kern). os.UserCacheDir() disagrees with that resolver
	// on macOS (~/Library/Caches vs ~/.cache), so using it here left
	// ~/.cache/kern behind on every uninstall (F-A8) while happily
	// "removing" a directory nothing ever wrote.
	if dir := cache.Dir(); filepath.IsAbs(dir) {
		targets = append(targets, DataTarget{
			Path: dir,
			Why:  "kern cache",
		})
	}
	return targets
}

// RemoveMachineData removes every MachineDataTarget. Safety contract:
//   - exact resolved paths only, never a filename search across the user's
//     filesystem;
//   - a symlinked target is refused and reported, never followed;
//   - ~/.kern must resolve inside the (resolved) home directory;
//   - each item is reported separately; one failure does not abort the rest,
//     but any failure means the uninstall is incomplete and the caller must
//     not report success.
func RemoveMachineData(dry bool) []setup.Status {
	home := setup.GlobalHomeDir()
	var out []setup.Status
	for _, t := range MachineDataTargets() {
		info, err := os.Lstat(t.Path)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				continue // never created on this machine
			}
			out = append(out, setup.Status{Agent: "machine-data", Path: t.Path, Note: err.Error()})
			continue
		}
		if info.Mode()&os.ModeSymlink != 0 {
			out = append(out, setup.Status{Agent: "machine-data", Skipped: true, Path: t.Path,
				Note: "left untouched: symlink — remove it yourself if it is kern's"})
			continue
		}
		if !info.IsDir() {
			out = append(out, setup.Status{Agent: "machine-data", Skipped: true, Path: t.Path,
				Note: "left untouched: not a directory"})
			continue
		}
		if filepath.Base(t.Path) == ".kern" && !resolvesUnderHome(home, t.Path) {
			out = append(out, setup.Status{Agent: "machine-data", Skipped: true, Path: t.Path,
				Note: fmt.Sprintf("left untouched: resolves outside the home directory (%s)", t.Path)})
			continue
		}
		if dry {
			out = append(out, setup.Status{Agent: "machine-data", Installed: true, Path: t.Path,
				Note: "[dry-run] would remove " + t.Path + " (" + t.Why + ")"})
			continue
		}
		if err := os.RemoveAll(t.Path); err != nil {
			out = append(out, setup.Status{Agent: "machine-data", Path: t.Path, Note: err.Error()})
			continue
		}
		out = append(out, setup.Status{Agent: "machine-data", Installed: true, Path: t.Path,
			Note: "removed " + t.Path + " (" + t.Why + ")"})
	}
	return out
}

// resolvesUnderHome reports whether path (symlinks in its parents resolved)
// stays inside home (itself resolved when possible). Both sides must be
// resolved the same way or a symlinked home (macOS /var -> /private/var)
// would produce false refusals. A path that does not exist is judged by its
// deepest existing ancestor.
func resolvesUnderHome(home, path string) bool {
	absHome, err1 := filepath.Abs(resolveBest(home))
	absPath, err2 := filepath.Abs(resolveBest(path))
	if err1 != nil || err2 != nil {
		return false
	}
	return absPath == absHome || strings.HasPrefix(absPath, absHome+string(os.PathSeparator))
}

// resolveBest resolves p, or when p does not exist, the deepest existing
// ancestor with the remainder kept literal.
func resolveBest(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		return resolved
	}
	dir, base := filepath.Split(p)
	if dir == "" || filepath.Clean(dir) == p {
		return p
	}
	return filepath.Join(resolveBest(filepath.Clean(dir)), base)
}
