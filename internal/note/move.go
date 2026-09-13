package note

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Move transitions a note to another lifecycle folder: it rewrites the
// status line (rejected requires a one-line reason; archived keeps
// "implemented" and inserts an `Archived: YYYY-MM-DD` marker after the
// mandatory blank third line) and relocates the file under the target
// lifecycle folder, preserving class and filename. It returns the new
// repo-relative path. Shared by the kern note CLI and kern_note MCP tool.
func Move(root, rel string, target Lifecycle, reason string, now time.Time) (string, error) {
	if err := ValidateLifecycle(target); err != nil {
		return "", err
	}
	abs := filepath.Join(root, filepath.FromSlash(rel))
	data, err := os.ReadFile(abs)
	if err != nil {
		return "", err
	}
	n, _ := ParseFile(abs)
	lines := strings.Split(string(data), "\n")
	switch target {
	case Rejected:
		reason = strings.TrimSpace(reason)
		if reason == "" {
			return "", fmt.Errorf("rejected requires a reason")
		}
		lines[1] = "Status: rejected — " + reason
	case Archived:
		lines[1] = "Status: implemented"
		archived := now.Format("2006-01-02")
		lines = append(lines[:3], append([]string{"Archived: " + archived}, lines[3:]...)...)
	default:
		lines[1] = "Status: " + string(target)
	}
	newRel := filepath.ToSlash(filepath.Join(TreeRelPath, string(target), string(n.Class), filepath.Base(abs)))
	newAbs := filepath.Join(root, filepath.FromSlash(newRel))
	if err := os.MkdirAll(filepath.Dir(newAbs), 0o755); err != nil {
		return "", err
	}
	if err := os.WriteFile(newAbs, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
		return "", err
	}
	if err := os.Remove(abs); err != nil {
		return "", err
	}
	return newRel, nil
}
