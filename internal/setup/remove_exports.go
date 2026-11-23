package setup

import (
	"github.com/JayveerPrajapati/kern/internal/agentwire"
)

// remove_exports.go — the exported surface internal/setupundo consumes.
// The removal family (RemoveGlobal/RemoveProject and the --remove UI) was
// extracted to internal/setupundo when setup hit its ARCHITECTURE.md cap
// (registered extraction candidate, 2026-11-10). These are thin delegators
// only, so the wire paths and the undo paths share ONE path resolution —
// a duplicate resolver here is how uninstall drift gets started.

// GlobalHomeDir is the home directory every global wiring path resolves
// against (honoring the test isolation env).
func GlobalHomeDir() string { return globalHomeDir() }

// GlobalOpencodePath is opencode's global config.json (the mcp merge).
func GlobalOpencodePath() string { return globalOpencodePath() }

// GlobalClaudePath / GlobalAGENTSPath are the global rules slots.
func GlobalClaudePath() string { return globalClaudePath() }

func GlobalAGENTSPath() string { return globalAGENTSPath() }

// KiroHooksPath is kiro's kern-owned global hooks file.
func KiroHooksPath() string { return kiroHooksPath() }

// ClaudeConfigPath / ClaudeHasKern back the `claude mcp remove` pass.
func ClaudeConfigPath() string { return claudeConfigPath() }

func ClaudeHasKern() bool { return claudeHasKern() }

// ContinueSettingsPath is the continue adapter's settings file.
func ContinueSettingsPath() string { return continueSettingsPath() }

// BackupFile is the pre-rewrite backup both wire and removal use.
func BackupFile(path string) error { return backupFile(path) }

// ReadShippedPlugin returns the embedded plugin asset.
func ReadShippedPlugin() ([]byte, error) { return pluginFS.ReadFile("assets/plugin/kern.ts") }

// SupportedAgentNames lists every agent name kern knows how to wire —
// the rejection set for --agents on the removal path.
func SupportedAgentNames() []string {
	names := make([]string, 0, len(agentDetectors))
	for _, d := range agentDetectors {
		names = append(names, d.agent)
	}
	return names
}

// AdapterRef is the exported view of one wiring adapter, resolved against
// root — exactly what the removal passes need (name for agent filtering,
// key for the config-strip entry, path for the file to edit).
type AdapterRef struct {
	Name  string
	Key   string
	Scope string // "global" or "repo"
	Path  string
}

// EffectiveAdapterRefs resolves the builtin + custom adapter registry
// against root (see effectiveAdapters; errors are dropped — the wire path
// surfaces them, removal of what exists does not need them).
func EffectiveAdapterRefs(root string) []AdapterRef {
	all, _ := effectiveAdapters(root)
	refs := make([]AdapterRef, 0, len(all))
	for _, a := range all {
		refs = append(refs, AdapterRef{Name: a.name, Key: a.key, Scope: a.scope, Path: a.path(root)})
	}
	return refs
}

// InstructionFiles maps each agent to its project-level instruction file.
var InstructionFiles = instructionFiles

// EditorExcludePaths returns the global editor settings files that
// wireEditorExclusions writes (VS Code, Cursor). The removal counterpart
// lives in setupundo; the paths must stay shared.
func EditorExcludePaths() []string {
	return []string{
		agentwire.HomeConfig(".cursor", "User", "settings.json")(""),
		agentwire.HomeConfig(".config", "Code", "User", "settings.json")(""),
	}
}
