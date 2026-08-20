package context

import (
	"crypto/sha256"
	"encoding/hex"
	"os/exec"
	"strings"
)

// gitDiff returns the working-tree diff for the given file relative to the
// project root, or "" when the diff is unavailable (not a git repo, no change,
// git not installed). A diff is only used as a FACT when it is actually
// available; its absence is never treated as an error.
func (e *Engine) gitDiff(rel string) string {
	if e.root == "" {
		return ""
	}
	// Silence stderr: not being in a git repo is expected in tests.
	cmd := exec.Command("git", "-C", e.root, "diff", "--", rel)
	out, err := cmd.Output()
	if err != nil {
		return ""
	}
	diff := strings.TrimSpace(string(out))
	if diff == "" {
		return ""
	}
	return diff
}

// digestOf returns a stable hex-encoded SHA-256 of the given content.
func digestOf(content string) string {
	sum := sha256.Sum256([]byte(content))
	return hex.EncodeToString(sum[:])
}
