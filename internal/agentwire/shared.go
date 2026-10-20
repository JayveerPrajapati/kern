package agentwire

import (
	"embed"
	"os"
	"path/filepath"
)

//go:embed assets/plugin/kern.ts
var pluginFS embed.FS

//go:embed assets/AGENTS.md
var rulesFS embed.FS

// PluginFS returns the embedded opencode plugin filesystem (the canonical
// kern.ts asset), for byte-comparisons against installed copies.
func PluginFS() embed.FS { return pluginFS }

// RulesFS returns the embedded canonical rules filesystem (assets/AGENTS.md).
func RulesFS() embed.FS { return rulesFS }

// PluginAsset returns the embedded canonical opencode plugin source.
// Exported so doctor can byte-compare installed copies against it without
// duplicating the embed.
func PluginAsset() ([]byte, error) {
	return pluginFS.ReadFile("assets/plugin/kern.ts")
}

// Status reports one agent's wiring state.
type Status struct {
	Agent     string
	Installed bool
	// Skipped marks a deliberate no-op (agent not installed, config dir
	// absent). It is not an error: setup must not exit non-zero for skips,
	// only for real failures.
	Skipped bool
	Path    string
	Note    string
}

// GlobalHomeDir resolves the user's home directory. It is a variable so tests
// can point it at a temp directory and never touch the real home.
var GlobalHomeDir = func() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return "."
	}
	return home
}

// GlobalPluginPaths returns every location the opencode plugin may be loaded
// from on this machine: the XDG config path (~/.config/opencode) and the
// config-root path (~/.opencode — opencode 1.18.x loads plugins from the
// config root, not the XDG dir; a stale copy there silently wins).
func GlobalPluginPaths() []string {
	paths := []string{GlobalPluginPath()}
	if h, err := os.UserHomeDir(); err == nil {
		paths = append(paths, filepath.Join(h, ".opencode", "plugins", "kern.ts"))
	}
	return paths
}

// GlobalPluginPath returns the path to the global opencode kern plugin.
func GlobalPluginPath() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		base = filepath.Join(GlobalHomeDir(), ".config")
	}
	return filepath.Join(base, "opencode", "plugins", "kern.ts")
}

// WritePerm returns the permission bits to use when writing a config-like
// file. Existing files keep their current permissions (configs with tokens are
// often 0600 and must not be widened); new files default to 0600.
func WritePerm(path string) os.FileMode {
	if info, err := os.Stat(path); err == nil {
		return info.Mode().Perm()
	}
	return 0o600
}

// InstructionMarkerOpen / InstructionMarkerClose bracket the kern-managed block
// in a per-agent instruction file (CLAUDE.md, GEMINI.md, etc.). They let us
// remove-and-rewrite the block on every setup run so an upgrade always writes
// fresh rules rather than skipping when stale rules are detected.
const (
	InstructionMarkerOpen  = "<!-- kern-instruction: managed by kern setup; do not edit between markers -->"
	InstructionMarkerClose = "<!-- /kern-instruction -->"
)
