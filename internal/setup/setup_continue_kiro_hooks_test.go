package setup

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// TestContinueSettingsPath verifies the Continue settings.json resolver order:
// $CONTINUE_GLOBAL_DIR wins, then the directory of an existing
// ~/.config/continue/config.json, then ~/.continue/settings.json.
func TestContinueSettingsPath(t *testing.T) {
	t.Run("CONTINUE_GLOBAL_DIR wins", func(t *testing.T) {
		withTempHome(t, false)
		dir := t.TempDir()
		t.Setenv("CONTINUE_GLOBAL_DIR", dir)
		want := filepath.Join(dir, "settings.json")
		if got := continueSettingsPath(); got != want {
			t.Errorf("continueSettingsPath() = %s, want %s", got, want)
		}
	})
	t.Run("config dir wins over fallback", func(t *testing.T) {
		home := withTempHome(t, false)
		if err := os.MkdirAll(filepath.Join(home, ".config", "continue"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(home, ".config", "continue", "config.json"), []byte("{}"), 0o644); err != nil {
			t.Fatal(err)
		}
		want := filepath.Join(home, ".config", "continue", "settings.json")
		if got := continueSettingsPath(); got != want {
			t.Errorf("continueSettingsPath() = %s, want %s", got, want)
		}
	})
	t.Run("fallback to .continue", func(t *testing.T) {
		home := withTempHome(t, false)
		want := filepath.Join(home, ".continue", "settings.json")
		if got := continueSettingsPath(); got != want {
			t.Errorf("continueSettingsPath() = %s, want %s", got, want)
		}
	})
}

// TestWireContinueHooks verifies the Continue PreToolUse hook writer: it
// resolves settings.json per the Continue contract, writes the
// matcher+hooks group referencing the guard script, is idempotent (no group
// duplication on re-run), and preserves unrelated settings.json keys.
func TestWireContinueHooks(t *testing.T) {
	home := withTempHome(t, false)

	// Pre-existing Continue config.json → settings.json resolves to
	// <home>/.config/continue/settings.json.
	cfgDir := filepath.Join(home, ".config", "continue")
	if err := os.MkdirAll(cfgDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cfgDir, "config.json"), []byte(`{"mcpServers":{}}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Pre-existing settings.json with unrelated keys must be preserved.
	settingsPath := filepath.Join(cfgDir, "settings.json")
	existing := map[string]any{
		"mcpServers": map[string]any{"kern": map[string]any{"type": "stdio"}},
		"userToken":  "keep-me",
	}
	b, err := json.Marshal(existing)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(settingsPath, b, 0o644); err != nil {
		t.Fatal(err)
	}

	st := wireContinueHooks()
	if !st.Installed {
		t.Fatalf("wireContinueHooks failed: %s", st.Note)
	}
	if st.Path != settingsPath {
		t.Fatalf("resolved settings path = %s, want %s", st.Path, settingsPath)
	}

	guard := filepath.Join(home, ".kern", "hooks", "kern-guard.sh")
	if _, err := os.Stat(guard); err != nil {
		t.Fatalf("guard script not written: %v", err)
	}

	var m map[string]any
	raw, err := os.ReadFile(settingsPath)
	if err != nil {
		t.Fatalf("settings.json not written at %s: %v", settingsPath, err)
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("settings.json is not valid JSON: %v\n%s", err, raw)
	}
	// Unrelated keys preserved.
	if m["userToken"] != "keep-me" {
		t.Errorf("userToken clobbered: %v", m["userToken"])
	}
	if _, ok := m["mcpServers"].(map[string]any); !ok {
		t.Error("mcpServers was clobbered by hook merge")
	}
	hooks, _ := m["hooks"].(map[string]any)
	pre, _ := hooks["PreToolUse"].([]any)
	if len(pre) != 1 {
		t.Fatalf("expected 1 PreToolUse group, got %d", len(pre))
	}
	group, _ := pre[0].(map[string]any)
	if group["matcher"] != "Bash|Read|Grep|Glob" {
		t.Errorf("matcher = %v, want Bash|Read|Grep|Glob", group["matcher"])
	}
	hs, _ := group["hooks"].([]any)
	if len(hs) != 1 {
		t.Fatalf("expected 1 hook entry, got %d", len(hs))
	}
	h, _ := hs[0].(map[string]any)
	if h["type"] != "command" {
		t.Errorf("hook type = %v, want command", h["type"])
	}
	if h["command"] != guard {
		t.Errorf("command = %v, want %s", h["command"], guard)
	}

	// Idempotent: a second run must not duplicate the kern group.
	if st := wireContinueHooks(); !st.Installed {
		t.Fatalf("second wireContinueHooks failed: %s", st.Note)
	}
	raw, err = os.ReadFile(settingsPath)
	if err != nil {
		t.Fatal(err)
	}
	m = map[string]any{}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	hooks, _ = m["hooks"].(map[string]any)
	if pre, _ := hooks["PreToolUse"].([]any); len(pre) != 1 {
		t.Fatalf("re-run duplicated PreToolUse groups: %d", len(pre))
	}
	if m["userToken"] != "keep-me" {
		t.Errorf("userToken clobbered on re-run: %v", m["userToken"])
	}
}

// TestWireKiroHooks verifies the Kiro hook writer: it creates
// ~/.kiro/hooks/kern-guard.json with the v1 shape (two PreToolUse hooks,
// matchers "read" and "shell") referencing the guard script, and is
// byte-idempotent on re-run.
func TestWireKiroHooks(t *testing.T) {
	home := withTempHome(t, false)

	st := wireKiroHooks()
	if !st.Installed {
		t.Fatalf("wireKiroHooks failed: %s", st.Note)
	}
	path := filepath.Join(home, ".kiro", "hooks", "kern-guard.json")
	if st.Path != path {
		t.Fatalf("hooks path = %s, want %s", st.Path, path)
	}
	guard := filepath.Join(home, ".kern", "hooks", "kern-guard.sh")
	if _, err := os.Stat(guard); err != nil {
		t.Fatalf("guard script not written: %v", err)
	}

	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("hooks file not written at %s: %v", path, err)
	}
	var m struct {
		Version string `json:"version"`
		Hooks   []struct {
			Name    string `json:"name"`
			Trigger string `json:"trigger"`
			Matcher string `json:"matcher"`
			Action  struct {
				Type    string `json:"type"`
				Command string `json:"command"`
			} `json:"action"`
		} `json:"hooks"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatalf("hooks file is not valid JSON: %v\n%s", err, raw)
	}
	if m.Version != "v1" {
		t.Errorf("version = %q, want v1", m.Version)
	}
	if len(m.Hooks) != 2 {
		t.Fatalf("expected 2 hooks, got %d", len(m.Hooks))
	}
	for i, want := range []string{"read", "shell"} {
		h := m.Hooks[i]
		if h.Name == "" {
			t.Errorf("hooks[%d].name is empty — Kiro requires a name per hook", i)
		}
		if h.Matcher != want {
			t.Errorf("hooks[%d].matcher = %q, want %q", i, h.Matcher, want)
		}
		if h.Trigger != "PreToolUse" {
			t.Errorf("hooks[%d].trigger = %q, want PreToolUse", i, h.Trigger)
		}
		if h.Action.Type != "command" {
			t.Errorf("hooks[%d].action.type = %q, want command", i, h.Action.Type)
		}
		if h.Action.Command != guard {
			t.Errorf("hooks[%d].action.command = %q, want %q", i, h.Action.Command, guard)
		}
	}

	// Idempotent: re-run must leave the file byte-identical.
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if st := wireKiroHooks(); !st.Installed {
		t.Fatalf("second wireKiroHooks failed: %s", st.Note)
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(before, after) {
		t.Error("re-run changed the hooks file — expected identical bytes")
	}
}

// TestWireKiroHooksRespectsKiroHome verifies the $KIRO_HOME override: hooks
// are written under $KIRO_HOME/hooks, not ~/.kiro/hooks.
func TestWireKiroHooksRespectsKiroHome(t *testing.T) {
	withTempHome(t, false)
	kiroHome := t.TempDir()
	t.Setenv("KIRO_HOME", kiroHome)

	st := wireKiroHooks()
	if !st.Installed {
		t.Fatalf("wireKiroHooks failed: %s", st.Note)
	}
	want := filepath.Join(kiroHome, "hooks", "kern-guard.json")
	if st.Path != want {
		t.Fatalf("hooks path = %s, want %s", st.Path, want)
	}
	if _, err := os.Stat(want); err != nil {
		t.Fatalf("hooks file not written at %s: %v", want, err)
	}
}
