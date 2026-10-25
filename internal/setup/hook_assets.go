package setup

import (
	_ "embed"
	"os"
	"path/filepath"
	"strings"
)

//go:embed assets/hooks/kern-guard.sh
var kernGuardScript string

// GuardScriptAsset returns the embedded kern-guard.sh the installer ships.
func GuardScriptAsset() []byte { return []byte(kernGuardScript) }

// GlobalGuardPath is where the machine-wide guard script is installed.
func GlobalGuardPath() string {
	return filepath.Join(globalHomeDir(), ".kern", "hooks", "kern-guard.sh")
}

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

// writeGuardScriptGlobal writes the kern-guard hook script to
// <home>/.kern/hooks/kern-guard.sh (where <home> is globalHomeDir) and returns
// its absolute path. Home-based agents (Qwen, Qoder, Codex) reference this
// global install from their ~/.<agent> configs so the guard is not tied to any
// single project's path.
func writeGuardScriptGlobal() (string, error) {
	return writeGuardScriptTo(filepath.Join(globalHomeDir(), ".kern", "hooks"))
}

// guardHookCommand returns the PreToolUse command hosts install: the pure-Go
// `kern guard hook` (one process — classify, serve and audit without
// sh/jq/shasum spawns; parity-pinned by guard_hook_parity_test.go) when the
// running kern binary can be resolved, with the shell script as the fallback
// when it cannot. The fallback script is always shipped to
// ~/.kern/hooks/kern-guard.sh either way (hosts may exec it via sh, and
// `kern remove` treats it as the shared piece).
func guardHookCommand() (string, error) {
	if _, err := writeGuardScriptGlobal(); err != nil {
		return "", err
	}
	if bin, err := os.Executable(); err == nil && bin != "" {
		base := filepath.Base(bin)
		if !strings.HasSuffix(bin, ".test") && !strings.HasSuffix(bin, ".test.exe") &&
			!strings.Contains(bin, "go-build") && !strings.Contains(base, "setup.test") {
			return bin + " guard hook", nil
		}
	}
	return "kern guard hook", nil
}

// wireGuardHook registers a single kern-guard PreToolUse hook group at path
// via mergeHookGroups. event is the hooks-object key (agents disagree on
// casing: Claude/Qwen/Qoder/Codex/Continue use "PreToolUse", Cursor/Copilot
// "preToolUse"); matcher selects the built-in tools to gate; note is the
// success Status text.
func wireGuardHook(agent, event, matcher, path, note string) Status {
	guardCmd, err := guardHookCommand()
	if err != nil {
		return Status{Agent: agent, Installed: false, Path: path, Note: err.Error()}
	}
	groups := map[string]any{
		event: []any{
			map[string]any{
				"matcher": matcher,
				"hooks": []any{
					map[string]any{"type": "command", "command": guardCmd},
				},
			},
		},
	}
	if err := mergeHookGroups(path, groups); err != nil {
		return Status{Agent: agent, Path: path, Note: err.Error()}
	}
	return Status{Agent: agent, Installed: true, Path: path, Note: note}
}
