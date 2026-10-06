package setup

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestPluginCompressedFooterGuarded pins F11 for the opencode plugin: the
// "[kern] compressed X -> Y chars" banner is a savings claim, so it must be
// emitted only when compression genuinely shrank the text — equal or
// inflated output ships bare. The compressedFooter predicate is extracted
// verbatim from the shipped plugin asset and executed under node (the same
// harness style as TestPluginShadowExemptionParity).
func TestPluginCompressedFooterGuarded(t *testing.T) {
	if _, err := exec.LookPath("node"); err != nil {
		t.Skipf("node not found: %v — skipping TS footer guard check", err)
	}
	src, err := pluginFS.ReadFile("assets/plugin/kern.ts")
	if err != nil {
		t.Fatal(err)
	}
	start := strings.Index(string(src), "function compressedFooter(")
	if start < 0 {
		t.Fatal("function compressedFooter not found in plugin — F11 guard removed?")
	}
	rest := string(src[start:])
	end := strings.Index(rest, "\n}")
	if end < 0 {
		t.Fatal("compressedFooter block end not found in plugin")
	}
	block := rest[:end+2]
	// Strip the TS type annotations so plain node can eval the block.
	block = strings.ReplaceAll(block, ": number", "")
	block = strings.ReplaceAll(block, ": string", "")
	nodeScript := filepath.Join(t.TempDir(), "footer.js")
	if err := os.WriteFile(nodeScript, []byte(block+`
const out = {
  reduced: compressedFooter(20000, 5000),
  equal: compressedFooter(20000, 20000),
  inflated: compressedFooter(20000, 21000),
};
process.stdout.write(JSON.stringify(out));
`), 0o644); err != nil {
		t.Fatal(err)
	}
	out, err := exec.Command("node", nodeScript).Output()
	if err != nil {
		t.Fatalf("node footer harness failed: %v", err)
	}
	var res struct {
		Reduced  string `json:"reduced"`
		Equal    string `json:"equal"`
		Inflated string `json:"inflated"`
	}
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatalf("decode node output: %v (%s)", err, out)
	}
	if res.Reduced != "[kern] compressed 20000 -> 5000 chars\n" {
		t.Errorf("reduced case must print the banner, got %q", res.Reduced)
	}
	if res.Equal != "" {
		t.Errorf("equal case must be silent, got %q", res.Equal)
	}
	if res.Inflated != "" {
		t.Errorf("inflated case must be silent, got %q", res.Inflated)
	}
}
