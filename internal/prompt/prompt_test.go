package prompt

import (
	"strings"
	"testing"
)

func TestList(t *testing.T) {
	names, err := List()
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"code-review", "fix-bug", "write-tests", "explain", "onboard", "debug"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("template %q missing from %v", want, names)
		}
	}
}

func TestRenderFillsVars(t *testing.T) {
	out, err := Render("code-review", map[string]string{
		"ROOT": "/proj",
		"MAP":  "src/main.go",
		"TASK": "focus on input validation",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "/proj") {
		t.Fatalf("ROOT not filled: %s", out)
	}
	if !strings.Contains(out, "src/main.go") {
		t.Fatalf("MAP not filled: %s", out)
	}
	if strings.Contains(out, "{{") {
		t.Fatalf("unfilled placeholders: %s", out)
	}
}

func TestRenderEmptyVarBecomesNA(t *testing.T) {
	out, err := Render("explain", map[string]string{
		"ROOT": "/proj",
		"MAP":  "a\nb\nc",
		"TASK": "",
	})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "(n/a)") {
		t.Fatalf("empty TASK should render as (n/a): %s", out)
	}
}

func TestRenderUnknown(t *testing.T) {
	if _, err := Render("does-not-exist", nil); err == nil {
		t.Fatal("expected error for unknown template")
	}
}
