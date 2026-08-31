package ids

import "testing"

func TestEscape(t *testing.T) {
	cases := []struct{ in, want string }{
		{"", ""},
		{"plain", "plain"},
		{"a:b", `a\:b`},
		{`a\b`, `a\\b`},
		{`a:b\c`, `a\:b\\c`},
	}
	for _, c := range cases {
		if got := Escape(c.in); got != c.want {
			t.Errorf("Escape(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestEscapePreventsAliasing(t *testing.T) {
	// Distinct untrusted inputs must never collide on the same ID, and the
	// encoding must be unambiguous so a crafted ':' cannot merge two nodes.
	if Escape("a:b") == Escape("a_b") {
		t.Fatal("colon must be escaped so a:b and a_b do not alias")
	}
	if Escape(`a\b`) == Escape(`a:b`) {
		t.Fatal("backslash and colon must remain distinguishable")
	}
}
