package meta

import "testing"

func TestTextAfterColon(t *testing.T) {
	for in, want := range map[string]string{
		"mask secrets in: token=sk-abc123": "token=sk-abc123",
		"mask secrets in:":                 "",
		"mask secrets":                     "",
		"mask: a: b":                       "a: b",
	} {
		if got := textAfterColon(in); got != want {
			t.Errorf("textAfterColon(%q) = %q, want %q", in, got, want)
		}
	}
}
