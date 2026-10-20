// execrun.go owns the canonical exec-run store shared by the `kern exec`
// CLI (this package) and the MCP kern_exec tool (internal/mcp/exec, which
// imports this package) — a single implementation instead of the
// byte-identical mirrors the two surfaces used to carry. Runs are kept
// in-memory as anchors (optimize.StoreAnchor) and mirrored to
// <root>/.kern/exec-runs/<anchor>.log so an anchor outlives the process;
// the on-disk mirror is pruned to maxExecRunsPerRoot entries (oldest by
// modification time) so a long-lived root cannot accumulate runs without
// bound.
package execution

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/optimize"
)

// maxExecRunsPerRoot caps the on-disk exec-run mirror per project root; the
// oldest runs (by modification time) are pruned once the cap is exceeded.
const maxExecRunsPerRoot = 128

// ExecRunsDir is where full outputs are persisted so an anchor outlives the
// process (mirrors verifycmd's run store).
func ExecRunsDir(root string) string {
	return filepath.Join(root, ".kern", "exec-runs")
}

// SaveExecRun persists the (already PII-masked) full output under its anchor,
// then prunes the run directory so at most maxExecRunsPerRoot entries remain.
func SaveExecRun(root, anchor, out string) {
	if anchor == "" {
		return
	}
	dir := ExecRunsDir(root)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	if err := os.WriteFile(filepath.Join(dir, anchor+".log"), []byte(out), 0o600); err != nil {
		return
	}
	pruneExecRuns(dir)
}

// pruneExecRuns deletes the oldest exec-run files (by modification time)
// until at most maxExecRunsPerRoot remain.
func pruneExecRuns(dir string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	if len(entries) <= maxExecRunsPerRoot {
		return
	}
	sort.Slice(entries, func(i, j int) bool {
		ii, ierr := entries[i].Info()
		jj, jerr := entries[j].Info()
		if ierr != nil || jerr != nil {
			// Entries whose stat fails sort last so they are never the
			// first removed; pruning is best-effort.
			return ierr == nil && jerr != nil
		}
		return ii.ModTime().Before(jj.ModTime())
	})
	for _, e := range entries[:len(entries)-maxExecRunsPerRoot] {
		_ = os.Remove(filepath.Join(dir, e.Name()))
	}
}

// LoadExecRun returns a kept run: the in-memory anchor first, then the
// persisted file. The anchor format is validated the same way verifycmd
// validates its own (no path traversal through a crafted id).
func LoadExecRun(root, anchor string) (string, error) {
	if !strings.HasPrefix(anchor, "anchor-") || strings.ContainsAny(anchor, `/\\.`) {
		return "", fmt.Errorf("invalid anchor")
	}
	if full, err := optimize.FetchAnchor(anchor); err == nil {
		return full, nil
	}
	b, err := os.ReadFile(filepath.Join(ExecRunsDir(root), anchor+".log"))
	if err != nil {
		return "", fmt.Errorf("anchor %s not found: rerun the command", anchor)
	}
	return string(b), nil
}
