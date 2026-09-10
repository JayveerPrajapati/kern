package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/JayveerPrajapati/kern/internal/config"
)

// writeConfigFixture writes a .kern/config.json under dir.
func writeConfigFixture(t *testing.T, dir, content string) {
	t.Helper()
	kernDir := filepath.Join(dir, ".kern")
	if err := os.MkdirAll(kernDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(kernDir, "config.json"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// TestRunConfigNoFileShowsDefaults locks the contract: with no config file
// `kern config` still exits successfully and lists every key with defaults.
func TestRunConfigNoFileShowsDefaults(t *testing.T) {
	out := captureStdout(t, func() { runConfig([]string{"--root", t.TempDir()}) })
	if !strings.Contains(out, "llm.provider=ollama (default)") {
		t.Fatalf("expected llm.provider default line, got:\n%s", out)
	}
	if !strings.Contains(out, "exec.risk=MEDIUM (default)") {
		t.Fatalf("expected exec.risk default line, got:\n%s", out)
	}
	// Env-only knobs (secrets + safety toggles) must not be listed.
	if strings.Contains(out, "KERN_AUTH_TOKEN") || strings.Contains(out, "KERN_TOOLS") {
		t.Fatalf("env-only knobs leaked into config output:\n%s", out)
	}
	// With no env and no file, every line must report the default source.
	if strings.Contains(out, "(env)") || strings.Contains(out, "(file)") {
		t.Fatalf("expected all-default output, got:\n%s", out)
	}
}

// TestRunConfigFileAndEnvSources locks env > file > default display.
func TestRunConfigFileAndEnvSources(t *testing.T) {
	root := t.TempDir()
	writeConfigFixture(t, root, `{"llm": {"provider": "anthropic"}, "exec": {"risk": "HIGH"}}`)
	t.Setenv("KERN_EXEC_RISK", "CRITICAL")
	t.Setenv("KERN_LLM_PROVIDER", "")
	out := captureStdout(t, func() { runConfig([]string{"--root", root}) })
	if !strings.Contains(out, "llm.provider=anthropic (file)") {
		t.Fatalf("expected file source for llm.provider, got:\n%s", out)
	}
	if !strings.Contains(out, "exec.risk=CRITICAL (env)") {
		t.Fatalf("expected env source for exec.risk, got:\n%s", out)
	}
}

// TestRunConfigJSON locks the --json shape {"key": {"value": ..., "source": ...}}.
func TestRunConfigJSON(t *testing.T) {
	root := t.TempDir()
	writeConfigFixture(t, root, `{"llm": {"provider": "openai"}, "mcp": {"roots": ["/a"]}}`)
	t.Setenv("KERN_LLM_PROVIDER", "")
	out := captureStdout(t, func() { runConfig([]string{"--root", root, "--json"}) })
	m := assertValidJSON(t, out)
	entry, ok := m["llm.provider"].(map[string]any)
	if !ok {
		t.Fatalf("llm.provider not an object: %v", m["llm.provider"])
	}
	if entry["value"] != "openai" || entry["source"] != "file" {
		t.Fatalf("llm.provider entry = %v, want value=openai source=file", entry)
	}
	if _, ok := m["mcp.roots"]; !ok {
		t.Fatalf("mcp.roots missing from JSON output: %v", m)
	}
	// Every registry key must be present in the JSON output.
	for _, k := range config.Registry {
		if _, ok := m[k.Key]; !ok {
			t.Fatalf("key %s missing from JSON output", k.Key)
		}
	}
}
