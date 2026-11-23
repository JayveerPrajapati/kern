package graphcli

import "testing"

// TestParseFlagsFormatAndMode pins the read-mode flags on the graph family:
// --format one-line (CLI parity with the MCP kern_graph format=one-line) and
// --mode, in both the spaced and --flag=value forms.
func TestParseFlagsFormatAndMode(t *testing.T) {
	f, rest, err := parseFlags([]string{"sym", "--format", "one-line", "--mode=concise"})
	if err != nil {
		t.Fatalf("parseFlags: %v", err)
	}
	if f.format != "one-line" {
		t.Fatalf("format = %q, want one-line", f.format)
	}
	if f.mode != "concise" {
		t.Fatalf("mode = %q, want concise", f.mode)
	}
	if len(rest) != 1 || rest[0] != "sym" {
		t.Fatalf("rest = %v, want [sym]", rest)
	}
}
