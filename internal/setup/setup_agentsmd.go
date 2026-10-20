package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
)

// agentsMDConfigPath is the project-local config file that remembers the
// --agents-md choice across runs. It lives under .kern/ (git-excluded,
// machine-local) alongside profiles.json. Only the "agents_md" key is
// kern-setup-owned; any other keys in the file are preserved.
func agentsMDConfigPath(root string) string {
	return filepath.Join(root, ".kern", "config.json")
}

// agentsMDMode resolves the repo AGENTS.md variant for a run: a missing or
// malformed config file and an absent key all fall back to "thin" — which
// agentwire.WireAgentRules then resolves PER FILE (agentwire.RulesVariant):
// thin when the corresponding host's global slot carries the kern-managed
// block, full otherwise. Only a persisted "full" forces the full variant.
func agentsMDMode(root string) string {
	b, err := os.ReadFile(agentsMDConfigPath(root))
	if err != nil {
		return "thin"
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "thin"
	}
	if s, ok := m["agents_md"].(string); ok && s == "full" {
		return "full"
	}
	return "thin"
}

// agentsMDPersisted reports whether the project has an explicit recorded
// agents_md choice in .kern/config.json. The thin default comes from an
// ABSENT key; a persisted value (either variant) is a user decision.
func agentsMDPersisted(root string) bool {
	b, err := os.ReadFile(agentsMDConfigPath(root))
	if err != nil {
		return false
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return false
	}
	_, ok := m["agents_md"]
	return ok
}

// setAgentsMD persists the chosen AGENTS.md variant so subsequent runs
// remember it (e.g. {"agents_md":"thin"}). It merges into any existing
// .kern/config.json — other keys are never touched — and skips the write
// when the value is already persisted.
func setAgentsMD(root, mode string) error {
	path := agentsMDConfigPath(root)
	m := map[string]any{}
	if b, err := os.ReadFile(path); err == nil {
		_ = json.Unmarshal(b, &m) // best-effort merge; malformed → start fresh
	}
	if m["agents_md"] == mode {
		return nil
	}
	m["agents_md"] = mode
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, append(data, '\n'), 0o600)
}
