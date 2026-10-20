package setup

import (
	"os"
	"strings"
	"testing"
)

// canonicalHostContract is the single source of truth for every supported
// guard host. A row records the integration mechanism, installed config path,
// guarded aliases, payload schema, doctor check, and user-facing docs entry.
type canonicalHostContract struct {
	host        string
	integration string
	configPath  string
	matcher     string
	schema      string
	doctorCheck string
}

var canonicalHostContracts = []canonicalHostContract{
	{host: "claude", integration: "PreToolUse hook", configPath: "~/.claude/settings.json", matcher: "Read|Grep|Glob|Bash", schema: "standard", doctorCheck: "claude hooks"},
	{host: "cursor", integration: "preToolUse hook", configPath: "~/.cursor/hooks.json", matcher: "Read|Grep|Glob|Bash", schema: "standard", doctorCheck: "cursor hooks"},
	{host: "copilot", integration: "preToolUse hook", configPath: "~/.copilot/hooks/kern-pretooluse.json", matcher: "Read|Grep|Glob|Bash", schema: "standard", doctorCheck: "copilot hooks"},
	{host: "gemini", integration: "BeforeTool hook", configPath: "~/.gemini/settings.json", matcher: "read_file|run_shell_command|grep|glob|list_files", schema: "gemini", doctorCheck: "gemini hooks"},
	{host: "antigravity", integration: "PreToolUse hook", configPath: "~/.gemini/config/hooks.json", matcher: "run_command|view_file|grep_search|find_by_name", schema: "antigravity", doctorCheck: "antigravity hooks"},
	{host: "qwen", integration: "PreToolUse hook", configPath: "~/.qwen/settings.json", matcher: "Read|Grep|Glob|Bash", schema: "standard", doctorCheck: "qwen hooks"},
	{host: "qoder", integration: "PreToolUse hook", configPath: "~/.qoder/settings.json", matcher: "Read|Grep|Glob|Bash", schema: "standard", doctorCheck: "qoder hooks"},
	{host: "codex", integration: "PreToolUse hook", configPath: "~/.codex/hooks.json", matcher: "Read|Grep|Glob|Bash", schema: "standard", doctorCheck: "codex hooks"},
	{host: "continue", integration: "PreToolUse hook", configPath: "~/.continue/settings.json or ~/.config/continue/settings.json", matcher: "Bash|Read|Grep|Glob", schema: "standard", doctorCheck: "continue hooks"},
	{host: "kiro", integration: "PreToolUse hooks", configPath: "~/.kiro/hooks/kern-guard.json", matcher: "read|shell", schema: "kiro", doctorCheck: "kiro hooks"},
	{host: "opencode", integration: "plugin shadow tools", configPath: "<repo>/.opencode/plugins/kern.ts", matcher: "read|grep|glob|bash", schema: "plugin", doctorCheck: "opencode guard"},
}

func TestCanonicalHostContractMatrix(t *testing.T) {
	docs, err := os.ReadFile("../../docs/governance-troubleshooting.md")
	if err != nil {
		t.Fatalf("read governance troubleshooting docs: %v", err)
	}
	doc := string(docs)

	setupSrc, err := os.ReadFile("setup.go")
	if err != nil {
		t.Fatalf("read setup doctor: %v", err)
	}
	setup := string(setupSrc)

	pluginSrc, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatalf("read opencode plugin: %v", err)
	}
	plugin := string(pluginSrc)

	seen := map[string]bool{}
	for _, hc := range canonicalHostContracts {
		if seen[hc.host] {
			t.Errorf("duplicate canonical host %q", hc.host)
		}
		seen[hc.host] = true

		// The docs table may use the product name, but every row carries the
		// canonical host id in parentheses (e.g. Claude Code (`claude`)).
		if !strings.Contains(doc, "(`"+hc.host+"`)") {
			t.Errorf("%s missing from docs host matrix", hc.host)
		}
		if !strings.Contains(doc, hc.configPath) {
			t.Errorf("%s config path %q missing from docs host matrix", hc.host, hc.configPath)
		}
		if !strings.Contains(doc, hc.integration) {
			t.Errorf("%s integration %q missing from docs host matrix", hc.host, hc.integration)
		}
		if !strings.Contains(setup, `"`+hc.doctorCheck+`"`) {
			t.Errorf("%s doctor check %q missing from Check", hc.host, hc.doctorCheck)
		}

		if hc.host == "opencode" {
			if !strings.Contains(plugin, `"tool.execute.after"`) {
				t.Errorf("opencode plugin lacks post-compression freshness marker")
			}
			for _, alias := range []string{"read: tool", "grep: tool", "glob: tool", "bash: tool"} {
				if !strings.Contains(plugin, alias) {
					t.Errorf("opencode plugin lacks shadow tool marker %q", alias)
				}
			}
			continue
		}

		for _, alias := range strings.Split(hc.matcher, "|") {
			lower := strings.ToLower(alias)
			if _, ok := guardFamilyTokens[lower]; !ok {
				t.Errorf("%s alias %q missing from guardFamilyTokens", hc.host, alias)
			}
			if !strings.Contains(kernGuardScript, lower) {
				t.Errorf("%s alias %q missing from kern-guard.sh", hc.host, alias)
			}
		}
	}
}
