package doctor

import (
	"strings"
	"testing"
)

func TestRunReturnsFindings(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", t.TempDir())
	t.Setenv("HOME", t.TempDir())
	t.Setenv("XDG_CACHE_HOME", t.TempDir())
	findings := Run(t.TempDir())
	if len(findings) == 0 {
		t.Fatal("no findings produced")
	}
	seen := map[string]bool{}
	for _, f := range findings {
		if f.Check == "" || f.Level == "" {
			t.Fatalf("malformed finding: %+v", f)
		}
		seen[f.Check] = true
	}
	for _, want := range []string{"binary", "index", "stats"} {
		if !seen[want] {
			t.Fatalf("missing check %q in %v", want, seen)
		}
	}
}

func TestRender(t *testing.T) {
	findings := []Finding{
		{Check: "binary", Level: "ok", Detail: "/x/kern"},
		{Check: "ollama", Level: "warn", Detail: "not reachable"},
		{Check: "index", Level: "fail", Detail: "no source files"},
	}
	out := Render("/tmp", findings)
	for _, want := range []string{"# kern doctor", "[ok]", "[warn]", "[fail]", "verdict: failures"} {
		if !strings.Contains(out, want) {
			t.Fatalf("render missing %q:\n%s", want, out)
		}
	}
}
