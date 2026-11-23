package agentwire

import (
	"os"
	"path/filepath"
	"testing"
)

func writeTestFile(t *testing.T, contents string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "settings.json")
	if err := os.WriteFile(p, []byte(contents), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// An MCP entry named "kern" is not a hook. This was the live Qwen false
// positive: FileStatus's substring check reported hooks present whenever MCP
// was configured.
func TestHookStatusDoesNotCountMCPEntryAsHook(t *testing.T) {
	p := writeTestFile(t, `{"mcpServers":{"kern":{"command":"kern-mcp"}}}`)
	st := HookStatus(p, "qwen hooks")
	if st.Installed {
		t.Fatalf("MCP-only config reported as hook: %+v", st)
	}
	if want := "file exists but no kern hook"; st.Note != want {
		t.Fatalf("note = %q, want %q", st.Note, want)
	}
}

func TestHookStatusDetectsRealAndForeignHooks(t *testing.T) {
	real := writeTestFile(t, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"kern guard hook"}]}]}}`)
	if st := HookStatus(real, "hooks"); !st.Installed {
		t.Fatalf("real kern hook not detected: %+v", st)
	}

	foreign := writeTestFile(t, `{"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"other guard"}]}]}}`)
	if st := HookStatus(foreign, "hooks"); st.Installed {
		t.Fatalf("foreign hook reported as kern: %+v", st)
	}
}
