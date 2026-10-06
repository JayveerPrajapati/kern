package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/strutil"
)

// agentsMDConfigPath is the project-local config file that remembers the
// --agents-md choice across runs. It lives under .kern/ (git-excluded,
// machine-local) alongside profiles.json. Only the "agents_md" key is
// kern-setup-owned; any other keys in the file are preserved.
func agentsMDConfigPath(root string) string {
	return filepath.Join(root, ".kern", "config.json")
}

// agentsMDMode resolves the repo AGENTS.md variant for a run: "thin" is the
// default (low startup tokens), so a missing or malformed config file and an
// absent key both fall back to "thin"; only a persisted "full" keeps the
// full variant.
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

// fullRulesHeader is the first line of the full AGENTS.md variant. Its
// presence in an existing file means the full usage rules are installed —
// replacing them with the thin stub is a destructive downgrade.
const fullRulesHeader = "# kern usage rules for agents — READ FIRST"

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

// thinAGENTSmd returns the thin repo AGENTS.md content: the critical
// kern-first routing facts in a few lines — kern_meta as the single entry
// point, the decision-table essentials (read→kern_explore, grep→kern_search,
// build/test→kern_verify, unsure→kern_meta), the session-start kern_buddy
// call, the default 6-tool surface, and the env vars that widen it. The full
// kern usage rules live in the host's GLOBAL instructions slot (managed by
// `kern setup --global-rules`), so a thin repo file avoids loading the same
// rules twice in one session on hosts that merge global + project rules.
func thinAGENTSmd(wired string) string {
	return strings.Join([]string{
		"# kern usage rules — thin (repo)",
		"",
		"Wired agents: " + wired,
		"kern is installed for this repo — call `kern_meta` FIRST for everything; it routes to the right kern_* tool.",
		"Kern-first routing: read → kern_explore · grep → kern_search · build/test → kern_verify · unsure → kern_meta.",
		"Session start: call `kern_buddy` FIRST — the onboarding digest (conventions, layout, entry points).",
		"Default surface: 6 tools (kern_meta, kern_explore, kern_impact, kern_search, kern_verify, kern_buddy); set KERN_MCP_FULL=1 for all 117 (KERN_MCP_PHASE for a phase subset).",
		"On opencode, built-in read/glob/grep/bash route to kern via the plugin shadows.",
	}, "\n") + "\n"
}

// wireThinRulesFile writes the thin variant to a single rule file, replacing
// an existing kern-managed THIN section (or writing fresh) while preserving
// user content outside it. A present FULL rules file is never downgraded
// unless the run carries an explicit thin choice (the --agents-md flag or a
// persisted .kern/config.json preference) — the default (absent key) must not
// silently delete the full usage rules. Idempotent: an unchanged thin block
// skips the write.
func wireThinRulesFile(root, name, wired string, explicitThin bool) Status {
	path := filepath.Join(root, name)
	content := ""
	if b, err := os.ReadFile(path); err == nil {
		content = string(b)
	}
	if !explicitThin && strings.Contains(content, fullRulesHeader) {
		return Status{Agent: name, Installed: true, Path: path, Note: "kept existing full rules (pass --agents-md thin to switch)"}
	}
	cleaned := strutil.RemoveMarkedBlock(content, instructionMarkerOpen, instructionMarkerClose)
	final, err := mergeAppend(cleaned, thinAGENTSmd(wired))
	if err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	if content != "" && final == content {
		return Status{Agent: name, Installed: true, Path: path, Note: "thin rules already current"}
	}
	if err := os.WriteFile(path, []byte(final), writePerm(path)); err != nil {
		return Status{Agent: name, Path: path, Note: err.Error()}
	}
	return Status{Agent: name, Installed: true, Path: path, Note: "thin rules written"}
}

// wireThinAgentRules writes the thin AGENTS.md variant to the repo AGENTS.md.
// explicitThin gates the destructive full→thin downgrade.
func wireThinAgentRules(root, wired string, explicitThin bool) Status {
	return wireThinRulesFile(root, "AGENTS.md", wired, explicitThin)
}
