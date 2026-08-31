package setup

import (
	_ "embed"
	"os"
	"path/filepath"
)

//go:embed assets/hooks/kern-guard.sh
var kernGuardScript string

// writeGuardScriptTo writes the kern-guard hook script to <dir>/kern-guard.sh
// and returns its absolute path. Agents' PreToolUse hooks call this script.
func writeGuardScriptTo(dir string) (string, error) {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", err
	}
	p := filepath.Join(dir, "kern-guard.sh")
	if err := os.WriteFile(p, []byte(kernGuardScript), 0o755); err != nil {
		return "", err
	}
	return p, nil
}

// writeGuardScript writes the kern-guard hook script to <root>/.kern/hooks/kern-guard.sh
// and returns its absolute path. Project-scoped agents (Claude, Gemini, Cursor,
// Copilot) install the guard into the project root.
func writeGuardScript(root string) (string, error) {
	return writeGuardScriptTo(filepath.Join(root, ".kern", "hooks"))
}

// writeGuardScriptGlobal writes the kern-guard hook script to
// <home>/.kern/hooks/kern-guard.sh (where <home> is os.UserHomeDir) and returns
// its absolute path. Home-based agents (Qwen, Qoder, Codex) reference this
// global install from their ~/.<agent> configs so the guard is not tied to any
// single project's path.
func writeGuardScriptGlobal() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return writeGuardScriptTo(filepath.Join(home, ".kern", "hooks"))
}
