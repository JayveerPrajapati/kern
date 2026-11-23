// Package guardtelemetry records privacy-safe decisions made by the guard's
// raw fast paths. Events stay project-local and never include paths, commands,
// file contents, roots, or environment values.
package guardtelemetry

import (
	"encoding/json"
	"os"
	"path/filepath"
	"time"

	"github.com/JayveerPrajapati/kern/internal/projectstate"
)

// ReadReason reports why a code-file read should pass raw: an exact line
// window, a missing file (so the host can report its real error), or a tiny
// regular file. It returns an empty string when the read must stay governed.
func ReadReason(window bool, path string) string {
	if path == "" {
		return ""
	}
	if window {
		return "window"
	}
	info, err := os.Stat(path)
	if os.IsNotExist(err) {
		return "missing"
	}
	if err == nil && info.Mode().IsRegular() && info.Size() <= 2048 {
		return "tiny"
	}
	return ""
}

// AppendFastPath records an intentional raw pass in the managed project's
// adoption ledger. Only the tool family and fixed reason enum are recorded.
func AppendFastPath(tool, reason, root string) {
	state := projectstate.Inspect(root)
	if !state.Managed || state.Root == "" || os.Getenv("KERN_ADOPTION_LOG") == "0" {
		return
	}
	p := filepath.Join(state.Root, ".kern", "adoption.log")
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return
	}
	f, err := os.OpenFile(p, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return
	}
	defer f.Close()
	line, err := json.Marshal(map[string]any{
		"ts":     time.Now().UnixMilli(),
		"tool":   tool,
		"routed": false,
		"reason": reason,
	})
	if err != nil {
		return
	}
	_, _ = f.Write(append(line, '\n'))
}
