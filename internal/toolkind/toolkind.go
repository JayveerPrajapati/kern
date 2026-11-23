package toolkind

import "os"

// CodeExt is the extension set of the read arm: files outside it serve raw.
var CodeExt = map[string]bool{
	".go": true, ".ts": true, ".tsx": true, ".js": true, ".jsx": true,
	".mjs": true, ".cjs": true, ".py": true, ".rs": true, ".java": true,
	".c": true, ".h": true, ".cpp": true, ".hpp": true, ".cc": true,
	".hh": true, ".rb": true, ".sh": true, ".bash": true, ".kt": true,
	".kts": true, ".swift": true, ".php": true, ".cs": true, ".scala": true,
	".lua": true, ".m": true, ".mm": true, ".sql": true, ".zig": true,
	".ex": true, ".exs": true, ".erl": true, ".hrl": true, ".hs": true,
	".clj": true, ".cljs": true, ".dart": true, ".vue": true, ".svelte": true,
}

func IsRead(t string) bool {
	switch t {
	case "read", "read_file", "view_file":
		return true
	}
	return false
}

func IsGrep(t string) bool {
	switch t {
	case "grep", "grep_file", "grep_search":
		return true
	}
	return false
}

func IsGlob(t string) bool {
	switch t {
	case "glob", "find_by_name", "list_files":
		return true
	}
	return false
}

func IsBash(t string) bool {
	switch t {
	case "bash", "run_shell_command", "shell", "execute_bash", "run_command":
		return true
	}
	return false
}

// ProjectRoot resolves the working root a host hook guards.
func ProjectRoot() string {
	if r := os.Getenv("CLAUDE_PROJECT_DIR"); r != "" {
		return r
	}
	if wd, err := os.Getwd(); err == nil {
		return wd
	}
	return "."
}
