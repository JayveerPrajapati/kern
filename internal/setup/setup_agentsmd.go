package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// agentsMDConfigPath is the project-local config file that remembers the
// --agents-md choice across runs. It lives under .kern/ (git-excluded,
// machine-local) alongside profiles.json. Only the "agents_md" key is
// kern-setup-owned; any other keys in the file are preserved.
func agentsMDConfigPath(root string) string {
	return filepath.Join(root, ".kern", "config.json")
}

// agentsMDMode resolves the repo AGENTS.md variant for a run: "thin" when the
// persisted .kern/config.json says so, else the default "full". A missing or
// malformed config file falls back to "full".
func agentsMDMode(root string) string {
	b, err := os.ReadFile(agentsMDConfigPath(root))
	if err != nil {
		return "full"
	}
	var m map[string]any
	if err := json.Unmarshal(b, &m); err != nil {
		return "full"
	}
	if s, ok := m["agents_md"].(string); ok && s == "thin" {
		return "thin"
	}
	return "full"
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

// thinAGENTSmd returns the opt-in thin repo AGENTS.md content: a title line,
// the repo wiring facts (wired agents list, .kern paths, index hint), and a
// single pointer line to the full rules. It stays under ~15 lines. The full
// kern usage rules live in the host's GLOBAL instructions slot (managed by
// `kern setup --global-rules`), so a thin repo file avoids loading the same
// rules twice in one session on hosts that merge global + project rules.
func thinAGENTSmd(wired string) string {
	return strings.Join([]string{
		"# kern usage rules — thin (repo)",
		"",
		"Wired agents: " + wired,
		"Local state: .kern/ (symbol index, skills, profiles.json) — git-excluded, machine-local.",
		"Index hint: run `kern index` (or `kern onboard`) to build/refresh the symbol index.",
		"",
		"Full kern usage rules live in your agent's global instructions (managed by kern setup --global-rules).",
	}, "\n") + "\n"
}

// wireThinAgentRules writes the thin AGENTS.md variant, replacing any
// existing kern-managed section (thin or full) while preserving user content
// outside it. Idempotent: an unchanged thin block skips the write.
func wireThinAgentRules(root, wired string) Status {
	path := filepath.Join(root, "AGENTS.md")
	content := ""
	if b, err := os.ReadFile(path); err == nil {
		content = string(b)
	}
	cleaned := removeMarkedBlock(content, instructionMarkerOpen, instructionMarkerClose)
	final := mergeAppend(cleaned, thinAGENTSmd(wired))
	if content != "" && final == content {
		return Status{Agent: "AGENTS.md", Installed: true, Path: path, Note: "thin rules already current"}
	}
	if err := os.WriteFile(path, []byte(final), writePerm(path)); err != nil {
		return Status{Agent: "AGENTS.md", Path: path, Note: err.Error()}
	}
	return Status{Agent: "AGENTS.md", Installed: true, Path: path, Note: "thin rules written"}
}
