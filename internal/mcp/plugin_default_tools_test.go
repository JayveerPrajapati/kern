package mcp

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginDefaultToolsMatchGoDefaultTools pins the opencode plugin's
// DEFAULT_TOOLS set to the Go defaultTools map: both must advertise exactly
// the same default surface. TestPluginMatchesMCPCatalog (internal/
// setup) pins the plugin set against the FULL catalog and the two plugin
// copies' byte-identity, and server_filter_test.go pins the Go set — but
// nothing asserted plugin-DEFAULT_TOOLS ≡ Go-defaultTools set-equality, so a
// same-wrong set drift on both sides (a renamed default tool) would pass CI.
// This closes that gap.
func TestPluginDefaultToolsMatchGoDefaultTools(t *testing.T) {
	t.Parallel()
	root := repoRoot(t)
	pluginPath := filepath.Join(root, "internal", "setup", "assets", "plugin", "kern.ts")
	b, err := os.ReadFile(pluginPath)
	if err != nil {
		t.Fatalf("read plugin asset: %v", err)
	}
	pluginTools, err := parsePluginDefaultTools(string(b))
	if err != nil {
		t.Fatal(err)
	}
	if len(pluginTools) != len(defaultTools) {
		t.Fatalf("plugin DEFAULT_TOOLS = %d entries, Go defaultTools = %d — default surface drift (want identical sets)", len(pluginTools), len(defaultTools))
	}
	for name := range defaultTools {
		if !pluginTools[name] {
			t.Fatalf("Go defaultTools entry %q missing from plugin DEFAULT_TOOLS", name)
		}
	}
	for name := range pluginTools {
		if !defaultTools[name] {
			t.Fatalf("plugin DEFAULT_TOOLS entry %q missing from Go defaultTools", name)
		}
	}
}

// parsePluginDefaultTools extracts the tool-name set from the plugin's
// `const DEFAULT_TOOLS = new Set([ ... ])` literal without running node: the
// set is a static literal, so a text scan is exact and hermetic.
func parsePluginDefaultTools(src string) (map[string]bool, error) {
	const marker = "const DEFAULT_TOOLS = new Set(["
	start := strings.Index(src, marker)
	if start < 0 {
		return nil, &parseError{"DEFAULT_TOOLS Set literal not found in plugin asset"}
	}
	body := src[start+len(marker):]
	end := strings.Index(body, "\n])")
	if end < 0 {
		return nil, &parseError{"DEFAULT_TOOLS Set literal not terminated (want a standalone `])` line)"}
	}
	out := map[string]bool{}
	for _, line := range strings.Split(body[:end], "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		parts := strings.SplitN(trimmed, `"`, 3)
		if len(parts) < 2 || !strings.HasPrefix(parts[1], "kern_") {
			return nil, &parseError{"unparsable DEFAULT_TOOLS entry: " + trimmed}
		}
		out[parts[1]] = true
	}
	return out, nil
}

type parseError struct{ msg string }

func (e *parseError) Error() string { return e.msg }
