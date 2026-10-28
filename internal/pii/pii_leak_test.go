package pii

import (
	"strings"
	"testing"
)

// TestMaskUnquotedDigitlessPasswordAndFullJWT pins the two QA-2026-10-26
// leaks: an unquoted digitless password after an assignment separator, and a
// JWT under "token=" where the shorter TOKEN match used to win the overlap
// and leave the payload/signature visible.
func TestMaskUnquotedDigitlessPasswordAndFullJWT(t *testing.T) {
	jwt := "eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U"
	cases := []struct {
		name string
		in   string
		leak string
	}{
		{"unquoted digitless password", "password=super-secret host=x", "super-secret"},
		{"unquoted digitless password alone", "password=correcthorsebatterystaple", "correcthorsebatterystaple"},
		{"quoted digitless password", `password="super-secret"`, "super-secret"},
		{"jwt under token", "token=" + jwt, ".eyJzdWIi"},
		{"jwt under secret", "secret=" + jwt, ".eyJzdWIi"},
	}
	for _, tc := range cases {
		res := Mask(tc.in)
		if strings.Contains(res.Text, tc.leak) {
			t.Errorf("%s: leaked %q in %q", tc.name, tc.leak, res.Text)
		}
	}
}
