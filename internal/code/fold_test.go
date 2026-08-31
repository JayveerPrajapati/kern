package code

import (
	"strings"
	"testing"
)

func TestFoldGoFile(t *testing.T) {
	src := `package main

// Add sums two ints.
func Add(a, b int) int {
	x := a + b
	return x
}

type Config struct {
	Host string
}

const MaxRetries = 3

func (c Config) Host() string {
	return c.Host
}

func main() {
	println(Add(1, 2))
}
`
	out := string(Fold("main.go", []byte(src)))

	// Bodies are elided with line-counted placeholders.
	for _, want := range []string{
		"// ... body elided: 2 lines ...", // Add body: x := ..., return x
		"// ... body elided: 1 lines ...", // Host body: return c.Host
		"// ... body elided: 1 lines ...", // main body: println(...)
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing placeholder %q in folded output:\n%s", want, out)
		}
	}
	if strings.Contains(out, "x := a + b") || strings.Contains(out, "println(Add(1, 2))") {
		t.Fatalf("function bodies must be elided:\n%s", out)
	}

	// Signatures (incl. methods), types and consts survive.
	for _, want := range []string{
		"func Add(a, b int) int {",
		"func (c Config) Host() string {",
		"func main() {",
		"type Config struct {",
		"const MaxRetries = 3",
		"// Add sums two ints.",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("expected %q to be preserved:\n%s", want, out)
		}
	}
}

func TestFoldNonGoFallback(t *testing.T) {
	// Python: indentation fold keeps the def signature and reports elided lines.
	py := []byte("def greet(name):\n    return \"hi \" + name\n\n\ndef other():\n    x = 1\n    return x\n")
	out := string(Fold("app.py", py))
	if !strings.Contains(out, "def greet(name):") || !strings.Contains(out, "def other():") {
		t.Fatalf("python def signatures must be preserved:\n%s", out)
	}
	if strings.Contains(out, `return "hi " + name`) {
		t.Fatalf("python body should be elided:\n%s", out)
	}
	if !strings.Contains(out, "# ... body elided: 1 lines ...") {
		t.Fatalf("python placeholder missing:\n%s", out)
	}
	if !strings.Contains(out, "# ... body elided: 2 lines ...") {
		t.Fatalf("python placeholder line count wrong:\n%s", out)
	}

	// JS: brace fold keeps the function signature.
	js := []byte("export function add(a, b) {\n  const total = a + b;\n  return total;\n}\n")
	out = string(Fold("util.js", js))
	if !strings.Contains(out, "export function add(a, b) {") {
		t.Fatalf("js signature must be preserved:\n%s", out)
	}
	if strings.Contains(out, "const total = a + b;") {
		t.Fatalf("js body should be elided:\n%s", out)
	}
	if !strings.Contains(out, "// ... body elided: 2 lines ...") {
		t.Fatalf("js placeholder missing:\n%s", out)
	}

	// Unknown language: content passes through untouched (fold unavailable).
	raw := []byte("just some text\nwith { braces }\n")
	if got := Fold("notes.txt", raw); string(got) != string(raw) {
		t.Fatalf("unknown-language files must pass through unchanged, got %q", got)
	}
}

func TestTierFullIsPassthrough(t *testing.T) {
	src := []byte("package main\n\nfunc A() {\n\tx := 1\n\treturn x\n}\n")
	if got := RenderTier("a.go", src, TierFull); got != string(src) {
		t.Fatalf("tier=full must return the original source unchanged, got:\n%s", got)
	}
	if got := RenderTier("a.go", src, TierFolded); got == string(src) {
		t.Fatalf("tier=folded must differ from the original source")
	}
	if got := RenderTier("a.go", src, TierSummary); got == string(src) || !strings.Contains(got, "func A") {
		t.Fatalf("tier=summary must return the symbolic summary, got:\n%s", got)
	}
}

func TestParseTier(t *testing.T) {
	cases := []struct {
		in   string
		want Tier
	}{
		{"", TierFull},
		{"full", TierFull},
		{"FULL", TierFull},
		{"folded", TierFolded},
		{"fold", TierFolded},
		{"summary", TierSummary},
		{"sum", TierSummary},
	}
	for _, c := range cases {
		got, err := ParseTier(c.in)
		if err != nil {
			t.Fatalf("ParseTier(%q) error: %v", c.in, err)
		}
		if got != c.want {
			t.Fatalf("ParseTier(%q) = %v, want %v", c.in, got, c.want)
		}
	}
	if _, err := ParseTier("bogus"); err == nil {
		t.Fatalf("ParseTier(bogus) should error")
	}
}
