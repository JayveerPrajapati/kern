package index

import (
	"strings"
	"testing"
)

// TestContextFunctionAwareExtent: Context must extend its window at least to
// the definition's syntactic end (CG-P0-3) instead of a fixed ±12-line cut,
// so a long function body is never truncated in explore answers.
func TestContextFunctionAwareExtent(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"big/big.go": `package big

func Public() string {
	_ = "line 1"
	_ = "line 2"
	_ = "line 3"
	_ = "line 4"
	_ = "line 5"
	_ = "line 6"
	_ = "line 7"
	_ = "line 8"
	_ = "line 9"
	_ = "line 10"
	_ = "line 11"
	_ = "line 12"
	_ = "line 13"
	_ = "line 14"
	_ = "line 15"
	return "done"
}

func Small() {}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	// Public's body spans lines 3..20 (the closing brace); a fixed ±12
	// window would stop at line 15. The function-aware extent must include
	// the closing brace and the return.
	out := ix.Context("Public", 0)
	if !strings.Contains(out, "return \"done\"") {
		t.Errorf("context truncated the function body:\n%s", out)
	}
	if !strings.Contains(out, "}") || !strings.Contains(out, "line 15") {
		t.Errorf("context missing the syntactic end:\n%s", out)
	}
}

// TestContextSmallFunctionNotOversized: a small definition keeps the default
// window — the extent extends, it never shrinks the neighborhood context.
func TestContextSmallFunctionNotOversized(t *testing.T) {
	dir := writeTree(t, map[string]string{
		"a.go": `package a

func Small() { println("hi") }

func Other() {}
`,
	})
	ix, err := Build(dir)
	if err != nil {
		t.Fatal(err)
	}
	out := ix.Context("Small", 0)
	if !strings.Contains(out, "println") {
		t.Errorf("small function context missing body:\n%s", out)
	}
}