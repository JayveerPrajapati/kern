package setup

import (
	"strings"
	"testing"
)

// The opencode plugin calls the kern CLI, which never triggers the MCP
// server's preload, so it must warm the index itself at load — through the
// guarded `buddy` path (not `kern index`, which is always allowed and would
// index $HOME) and honoring the same opt-outs as the shell guard.
func TestPluginPrewarmsThroughGuardedPathWithOptOuts(t *testing.T) {
	src, err := PluginAsset()
	if err != nil {
		t.Fatal(err)
	}
	text := string(src)
	for _, want := range []string{
		`run(["buddy"]`,
		`process.env.KERN_PRELOAD !== "0"`,
		`process.env.KERN_DISABLED !== "1"`,
		`resolve(directory, ".kern", "off")`,
	} {
		if !strings.Contains(text, want) {
			t.Errorf("plugin pre-warm missing %q", want)
		}
	}
	if strings.Contains(text, `run(["index"`) {
		t.Error("the pre-warm must not use `kern index` (explicit index bypasses the safe-root guard)")
	}
}
