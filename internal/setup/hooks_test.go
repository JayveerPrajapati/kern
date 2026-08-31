package setup

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestHookWriters exercises all seven pre-tool guard hook writers: each must
// write its config file at the expected path, reference the kern-guard.sh
// script, install the script under ~/.kern/hooks (global), emit valid JSON, and
// never duplicate the hook entry on a re-run. All writers are now global
// (user-scope): they write to ~/.<agent>/... and ~/.kern/hooks/kern-guard.sh,
// so every test case sets HOME to a temp dir.
func TestHookWriters(t *testing.T) {
	cases := []struct {
		name   string
		write  func(root string) Status // root is ignored by global writers; kept for signature compat
		config string                   // config path relative to HOME
	}{
		{
			name:   "claude",
			write:  func(root string) Status { return wireClaudeHooks("/x/kern") },
			config: filepath.Join(".claude", "settings.json"),
		},
		{
			name:   "gemini",
			write:  func(root string) Status { return wireGeminiHooks("/x/kern") },
			config: filepath.Join(".gemini", "settings.json"),
		},
		{
			name:   "cursor",
			write:  func(root string) Status { return wireCursorHooks() },
			config: filepath.Join(".cursor", "hooks.json"),
		},
		{
			name:   "copilot",
			write:  func(root string) Status { return wireCopilotHooks() },
			config: filepath.Join(".copilot", "hooks", "kern-pretooluse.json"),
		},
		{
			name:   "qwen",
			write:  func(root string) Status { return wireQwenHooks(root) },
			config: filepath.Join(".qwen", "settings.json"),
		},
		{
			name:   "qoder",
			write:  func(root string) Status { return wireQoderHooks(root) },
			config: filepath.Join(".qoder", "settings.json"),
		},
		{
			name:   "codex",
			write:  func(root string) Status { return wireCodexHooks(root) },
			config: filepath.Join(".codex", "hooks.json"),
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			// All writers are now global: they resolve ~ via os.UserHomeDir;
			// point HOME at the temp dir so the real home is never touched.
			t.Setenv("HOME", dir)

			st := tc.write(dir)
			if !st.Installed {
				t.Fatalf("writer failed: %s", st.Note)
			}

			// The guard script must have been written into HOME/.kern/hooks.
			guard := filepath.Join(dir, ".kern", "hooks", "kern-guard.sh")
			fi, err := os.Stat(guard)
			if err != nil {
				t.Fatalf("guard script not written: %v", err)
			}
			if fi.Mode().Perm()&0o111 == 0 {
				t.Fatalf("guard script not executable: %v", fi.Mode())
			}

			// The config file must exist at the expected path and reference
			// the guard script.
			cfgPath := filepath.Join(dir, tc.config)
			b, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("config not written at %s: %v", cfgPath, err)
			}
			content := string(b)
			if !strings.Contains(content, "kern-guard.sh") {
				t.Fatalf("config does not reference kern-guard.sh:\n%s", content)
			}
			var m map[string]any
			if err := json.Unmarshal(b, &m); err != nil {
				t.Fatalf("config is not valid JSON: %v\n%s", err, content)
			}

			// Idempotent: a second run must not duplicate the hook entry.
			if st := tc.write(dir); !st.Installed {
				t.Fatalf("second run failed: %s", st.Note)
			}
			b2, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("config not written on re-run at %s: %v", cfgPath, err)
			}
			if got := strings.Count(string(b2), "kern-guard.sh"); got != 1 {
				t.Fatalf("hook entry duplicated on re-run: %d kern-guard.sh refs\n%s", got, b2)
			}
		})
	}
}

// TestGlobalHooksReferenceHomeGuardScript verifies ALL hook writers (claude,
// gemini, cursor, copilot, qwen, qoder, codex) install the kern-guard script
// under the user HOME (~/.kern/hooks), not the project root, and that each
// generated config references the absolute HOME path so the guard is not tied
// to any single project.
func TestGlobalHooksReferenceHomeGuardScript(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	root := t.TempDir() // a different project root — must NOT receive the guard

	agents := []struct {
		name   string
		write  func(root string) Status
		config string // path relative to HOME
	}{
		{name: "claude", write: func(r string) Status { return wireClaudeHooks("/x/kern") }, config: filepath.Join(".claude", "settings.json")},
		{name: "gemini", write: func(r string) Status { return wireGeminiHooks("/x/kern") }, config: filepath.Join(".gemini", "settings.json")},
		{name: "cursor", write: func(r string) Status { return wireCursorHooks() }, config: filepath.Join(".cursor", "hooks.json")},
		{name: "copilot", write: func(r string) Status { return wireCopilotHooks() }, config: filepath.Join(".copilot", "hooks", "kern-pretooluse.json")},
		{name: "qwen", write: wireQwenHooks, config: filepath.Join(".qwen", "settings.json")},
		{name: "qoder", write: wireQoderHooks, config: filepath.Join(".qoder", "settings.json")},
		{name: "codex", write: wireCodexHooks, config: filepath.Join(".codex", "hooks.json")},
	}
	for _, tc := range agents {
		t.Run(tc.name, func(t *testing.T) {
			if st := tc.write(root); !st.Installed {
				t.Fatalf("writer failed: %s", st.Note)
			}

			// The guard script must live in the HOME dir, not the project root.
			homeGuard := filepath.Join(home, ".kern", "hooks", "kern-guard.sh")
			fi, err := os.Stat(homeGuard)
			if err != nil {
				t.Fatalf("guard script not written to HOME: %v", err)
			}
			if fi.Mode().Perm()&0o111 == 0 {
				t.Fatalf("guard script not executable: %v", fi.Mode())
			}
			if _, err := os.Stat(filepath.Join(root, ".kern", "hooks", "kern-guard.sh")); err == nil {
				t.Fatalf("guard script must not be written to project root %s", root)
			}

			// The config must reference the absolute HOME path and not the root.
			cfgPath := filepath.Join(home, tc.config)
			b, err := os.ReadFile(cfgPath)
			if err != nil {
				t.Fatalf("config not written at %s: %v", cfgPath, err)
			}
			content := string(b)
			if !strings.Contains(content, homeGuard) {
				t.Fatalf("config does not reference HOME guard %q:\n%s", homeGuard, content)
			}
			if strings.Contains(content, root) {
				t.Fatalf("config must not reference project root %q:\n%s", root, content)
			}
		})
	}
}

// TestGitignoreGeneratedCoversNewAgents verifies the setup-generated
// .gitignore block ignores every machine-specific agent config, including the
// newer .continue/ and .windsurf/ rule files.
func TestGitignoreGeneratedCoversNewAgents(t *testing.T) {
	dir := t.TempDir()
	st := gitignoreGenerated(dir)
	if !st.Installed {
		t.Fatalf("gitignore update failed: %s", st.Note)
	}
	b, err := os.ReadFile(filepath.Join(dir, ".gitignore"))
	if err != nil {
		t.Fatalf("gitignore not written: %v", err)
	}
	for _, want := range []string{".continue/", ".windsurf/", ".kern/", ".gemini/", ".kiro/", ".github/hooks/"} {
		if !strings.Contains(string(b), want) {
			t.Errorf(".gitignore missing %q:\n%s", want, b)
		}
	}
}
