package code

import (
	"bytes"
	"strings"
	"testing"
)

func TestFoldContentGoFoldsBodies(t *testing.T) {
	src := `package main

// compute does work.
func compute() int {
	a := 1
	b := 2
	c := a + b
	d := c * 2
	e := d - 1
	f := e / 2
	g := f + 10
	h := g * g
	i := h - 100
	j := i + 5
	k := j * 3
	l := k / 7
	m := l + 42
	n := m * m
	o := n - 1
	p := o + 2
	q := p - 3
	r := q * 4
	s := r + 8
	t := s * 9
	u := t / 2
	return u
}

type Config struct {
	Host string
	Port int
}
`
	in := []byte(src)
	out := FoldContent(in)
	if bytes.Equal(out, in) {
		t.Fatalf("Go source must be folded, output identical to input")
	}
	if !strings.Contains(string(out), "func compute() int {") {
		t.Fatalf("function signature must be preserved:\n%s", out)
	}
	if !strings.Contains(string(out), "body elided") {
		t.Fatalf("elided-body marker missing:\n%s", out)
	}
	if strings.Contains(string(out), "return u") || strings.Contains(string(out), "a := 1") {
		t.Fatalf("function body must be elided:\n%s", out)
	}
	if !strings.Contains(string(out), "type Config struct {") {
		t.Fatalf("type declaration must survive the fold:\n%s", out)
	}
}

func TestFoldContentPythonIndentFold(t *testing.T) {
	src := "def greet(name):\n    message = \"hello \" + name\n    print(message)\n    return message\n"
	out := FoldContent([]byte(src))
	if !strings.Contains(string(out), "def greet(name):") {
		t.Fatalf("python def signature must be preserved:\n%s", out)
	}
	if !strings.Contains(string(out), "body elided") {
		t.Fatalf("elided-body marker missing for python:\n%s", out)
	}
	if strings.Contains(string(out), "print(message)") {
		t.Fatalf("python body must be elided:\n%s", out)
	}
}

func TestFoldContentShellFunction(t *testing.T) {
	src := "foo() {\n\techo one\n\techo two\n\techo three\n\techo four\n\techo five\n}\n"
	out := FoldContent([]byte(src))
	if bytes.Equal(out, []byte(src)) {
		t.Fatalf("shell function must be folded")
	}
	if len(out) >= len(src) {
		t.Fatalf("folded output must be shorter than input")
	}
	if !strings.Contains(string(out), "foo() {") {
		t.Fatalf("shell function signature must be preserved:\n%s", out)
	}
	if !strings.Contains(string(out), "body elided") {
		t.Fatalf("elided-body marker missing for shell:\n%s", out)
	}
}

func TestFoldContentUnknownUnchanged(t *testing.T) {
	cases := []string{
		"the quick brown fox jumps over the lazy dog\nwhile sleeping, the cat dreamed of fish\n",
		`{"name": "kern", "version": 42, "tags": ["a", "b"], "nested": {"ok": true}}`,
		"INFO worker-3 started at 10:00:00 with pid 1234\nERROR connection refused to 127.0.0.1:8080, retrying\nWARN disk usage at 92%\n",
	}
	for _, c := range cases {
		in := []byte(c)
		if out := FoldContent(in); !bytes.Equal(out, in) {
			t.Fatalf("non-code text must pass through unchanged, got:\n%s", out)
		}
	}
}

func TestFoldContentYamlUnchanged(t *testing.T) {
	src := "name: kern\nversion: \"1.0\"\nservices:\n  - name: api\n    port: 8080\n"
	in := []byte(src)
	if out := FoldContent(in); !bytes.Equal(out, in) {
		t.Fatalf("yaml is not in the foldable set and must pass through unchanged, got:\n%s", out)
	}
}

func TestFoldContentDeterministic(t *testing.T) {
	src := []byte("package main\n\nfunc alpha() int {\n\tx := 1\n\treturn x\n}\n\ndef python_fn():\n    pass\n")
	first := FoldContent(src)
	second := FoldContent(src)
	if !bytes.Equal(first, second) {
		t.Fatalf("FoldContent must be deterministic:\nfirst:\n%s\nsecond:\n%s", first, second)
	}
}
