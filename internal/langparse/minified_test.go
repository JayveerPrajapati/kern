package langparse

import (
	"strings"
	"testing"
)

func TestIsMinified(t *testing.T) {
	cases := []struct {
		name string
		src  string
		want bool
	}{
		{
			name: "normal js short lines",
			src:  "function foo() {\n  const x = 1;\n  return x;\n}\n",
			want: false,
		},
		{
			name: "minified single 10000-char line",
			src:  strings.Repeat("a", 10000) + "\n",
			want: true,
		},
		{
			name: "minified many 600-char lines",
			src:  strings.Repeat("x", 600) + "\n" + strings.Repeat("y", 600) + "\n" + strings.Repeat("z", 600) + "\n" + strings.Repeat("w", 600) + "\n" + strings.Repeat("v", 600) + "\n" + strings.Repeat("u", 600) + "\n" + strings.Repeat("t", 600) + "\n" + strings.Repeat("s", 600) + "\n" + strings.Repeat("r", 600) + "\n" + strings.Repeat("q", 600) + "\n" + strings.Repeat("p", 600) + "\n",
			want: true,
		},
		{
			name: "empty file",
			src:  "",
			want: false,
		},
		{
			name: "python normal lines",
			src:  "def hello():\n    print('hi')\n",
			want: false,
		},
		{
			name: "few 600-char lines not enough",
			src:  strings.Repeat("x", 600) + "\n" + strings.Repeat("y", 600) + "\n" + strings.Repeat("z", 600) + "\n",
			want: false,
		},
	}
	for _, c := range cases {
		if got := isMinified([]byte(c.src)); got != c.want {
			t.Errorf("%s: isMinified = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestIsIndexableLongLineGoAndMarkdown guards the isMinified language gate:
// a Go dispatch table or generated Markdown catalog with many 500+ char lines
// is legitimate source and must stay indexable, while a genuinely minified
