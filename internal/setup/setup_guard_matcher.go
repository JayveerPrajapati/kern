package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
)

// guardFamilies are the four tool families the kern-guard script gates. Every
// host wiring's PreToolUse matcher must cover all four; a matcher that drops
// one (e.g. codex's former Bash-only matcher) silently leaves those built-in
// tools unguarded on that host while the agent believes the guard applies.
var guardFamilies = []string{"read", "grep", "glob", "bash"}

// guardFamilyTokens maps hook matcher tokens (lowercased) to the guard
// families they cover, spanning every host's matcher vocabulary: the standard
// Read|Grep|Glob|Bash names (Claude, Cursor, Copilot, Qwen, Qoder, Codex,
// Continue), Gemini's read_file/run_shell_command/glob/list_files, and
// Antigravity's run_command/view_file/grep_search/find_by_name.
var guardFamilyTokens = map[string][]string{
	"read":              {"read"},
	"read_file":         {"read"},
	"view_file":         {"read"},
	"grep":              {"grep"},
	"grep_file":         {"grep"},
	"grep_search":       {"grep"},
	"glob":              {"glob"},
	"find_by_name":      {"glob"},
	"list_files":        {"glob"},
	"bash":              {"bash"},
	"run_shell_command": {"bash"},
	"shell":             {"bash"},
	"execute_bash":      {"bash"},
	"run_command":       {"bash"},
}

// checkHookMatcherCompleteness reports hosts whose installed kern-guard hook
// matcher covers fewer tool families than the guard script gates. A matcher
// that silently dropped a family — e.g. wireCodexHooks used to write "Bash"
// only, leaving Read/Grep/Glob unguarded on Codex — is exactly the drift this
// check exists to surface, and it is why every wire*Hooks writer now emits the
// full four-family matcher. The check reads the INSTALLED config, not the
// writer constants, so it catches manual edits and stale installs too.
func checkHookMatcherCompleteness(root string) []Status {
	hosts := []struct {
		agent string
		paths []string // candidate config paths; first existing wins
		kiro  bool     // kiro's "read" matcher is its Read/Grep/Glob category
	}{
		{"claude", []string{filepath.Join(root, ".claude", "settings.json"), filepath.Join(globalHomeDir(), ".claude", "settings.json")}, false},
		{"gemini", []string{filepath.Join(root, ".gemini", "settings.json"), filepath.Join(globalHomeDir(), ".gemini", "settings.json")}, false},
		{"cursor", []string{filepath.Join(root, ".cursor", "hooks.json"), filepath.Join(globalHomeDir(), ".cursor", "hooks.json")}, false},
		{"copilot", []string{filepath.Join(root, ".github", "hooks", "kern-pretooluse.json"), filepath.Join(globalHomeDir(), ".copilot", "hooks", "kern-pretooluse.json")}, false},
		{"qwen", []string{homeConfig(".qwen", "settings.json")("")}, false},
		{"qoder", []string{homeConfig(".qoder", "settings.json")("")}, false},
		{"codex", []string{homeConfig(".codex", "hooks.json")("")}, false},
		{"continue", []string{continueSettingsPath()}, false},
		{"kiro", []string{kiroHooksPath()}, true},
		{"antigravity", []string{filepath.Join(root, ".agents", "hooks.json"), filepath.Join(globalHomeDir(), ".gemini", "config", "hooks.json")}, false},
	}

	var out []Status
	for _, h := range hosts {
		path := ""
		for _, p := range h.paths {
			if _, err := os.Stat(p); err == nil {
				path = p
				break
			}
		}
		if path == "" {
			continue // absent — the fileStatus lines in Check already report presence
		}
		data, err := os.ReadFile(path)
		if err != nil {
			continue
		}
		var cfg any
		if json.Unmarshal(data, &cfg) != nil {
			continue // invalid JSON is not this check's job
		}
		matchers := collectGuardMatchers(cfg)
		if len(matchers) == 0 {
			continue // no kern-guard group — not a kern-managed hook config
		}

		covered := map[string]bool{}
		for _, m := range matchers {
			for _, tok := range strings.Split(m, "|") {
				tok = strings.ToLower(strings.TrimSpace(tok))
				if h.kiro && tok == "read" {
					// Kiro's "read" matcher is its Read/Grep/Glob category
					// (see wireKiroHooks), not the guard script's literal
					// "read" tool name.
					covered["read"], covered["grep"], covered["glob"] = true, true, true
					continue
				}
				for _, f := range guardFamilyTokens[tok] {
					covered[f] = true
				}
			}
		}
		var missing []string
		for _, f := range guardFamilies {
			if !covered[f] {
				missing = append(missing, f)
			}
		}
		if len(missing) == 0 {
			continue
		}
		out = append(out, Status{
			Agent:     h.agent + " hooks matcher",
			Installed: true,
			Path:      path,
			Note:      "WARN: kern-guard hook matcher covers only " + strings.Join(coveredFamilies(covered), ", ") + " — missing " + strings.Join(missing, ", ") + "; run kern setup --global",
		})
	}
	return out
}

// coveredFamilies returns the covered guard families in canonical order.
func coveredFamilies(covered map[string]bool) []string {
	var out []string
	for _, f := range guardFamilies {
		if covered[f] {
			out = append(out, f)
		}
	}
	return out
}

// collectGuardMatchers returns the matcher strings of every hook group whose
// subtree references the kern-guard script. It walks the host config shapes
// generically: Claude/Gemini/Qwen/Qoder/Codex/Continue settings.json
// (hooks.<event>[].matcher with hooks[].command), Cursor/Copilot hooks.json
// (preToolUse), Kiro's flat hooks[] (action.command), and Antigravity's
// kern-guard.<event>[] nesting.
func collectGuardMatchers(v any) []string {
	var out []string
	var walk func(any) bool // true when the subtree references kern-guard.sh
	walk = func(n any) bool {
		switch t := n.(type) {
		case string:
			return strings.Contains(t, "kern-guard.sh")
		case []any:
			ref := false
			for _, child := range t {
				if walk(child) {
					ref = true
				}
			}
			return ref
		case map[string]any:
			ref := false
			for _, child := range t {
				if walk(child) {
					ref = true
				}
			}
			if ref {
				if m, ok := t["matcher"].(string); ok && m != "" {
					out = append(out, m)
				}
			}
			return ref
		default:
			return false
		}
	}
	walk(v)
	return out
}
