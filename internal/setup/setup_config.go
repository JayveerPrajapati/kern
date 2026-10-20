package setup

import (
	"os"
	"path/filepath"

	"github.com/JayveerPrajapati/kern/internal/agentwire"
	"github.com/JayveerPrajapati/kern/internal/configstrip"
)

// globalConfig resolves a path under the user config dir.
func globalConfig(sub ...string) func(string) string {
	return agentwire.GlobalConfig(sub...)
}

// projectConfig resolves a path inside the project root.
func projectConfig(sub ...string) func(string) string {
	return agentwire.ProjectConfig(sub...)
}

// homeConfig resolves a path directly under the user home dir (used by agents
// that keep their config at ~/.name rather than under ~/.config).
func homeConfig(sub ...string) func(string) string {
	return agentwire.HomeConfig(sub...)
}

func homeJoin(rel string) string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", rel)
	}
	return filepath.Join(home, rel)
}

// mergeJSON reads a JSON (or JSONC) file, inserts entry under key, and writes
// it back. Implemented in internal/agentwire (MergeJSON); this delegator
// keeps the setup-internal call sites and tests unchanged.
func mergeJSON(path, key string, entry map[string]any) error {
	return agentwire.MergeJSON(path, key, entry)
}

// stripJSONC removes // line comments and /* block */ comments from JSON data,
// while preserving strings that contain those sequences. Implemented in
// internal/configstrip (StripJSONC).
func stripJSONC(data []byte) []byte {
	return configstrip.StripJSONC(data)
}

func fileStatus(path, label string) Status {
	return agentwire.FileStatus(path, label)
}

func fileStatusAny(label string, paths ...string) Status {
	return agentwire.FileStatusAny(label, paths...)
}

func claudeConfigPath() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".claude.json")
}

// globalOpencodePath resolves the global opencode config file. Implemented in
// internal/agentwire (GlobalOpencodePath).
func globalOpencodePath() string {
	return agentwire.GlobalOpencodePath()
}
