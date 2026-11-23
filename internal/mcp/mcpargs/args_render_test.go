package mcpargs

import (
	"strings"
	"testing"
)

// TestArgStringRendersNumbersPositionally (audit F7, 2026-11-21): JSON
// numbers must not reach integer parsers in exponent notation — the old %v
// rendering produced the self-contradictory error `invalid integer
// "9.99999999e+08"` for limit=999999999.
func TestArgStringRendersNumbersPositionally(t *testing.T) {
	cases := map[float64]string{
		999999999:  "999999999",
		123:        "123",
		2.5:        "2.5",
		1e9:        "1000000000",
		1500000000: "1500000000",
	}
	for in, want := range cases {
		if got := ArgString(map[string]any{"k": in}, "k"); got != want {
			t.Errorf("ArgString(%v) = %q, want %q", in, got, want)
		}
	}
	// The downstream parse must see the exact integer the client sent.
	v := ArgString(map[string]any{"k": 999999999}, "k")
	n, err := AtoiArg(v, 10)
	if err != nil || n != 999999999 {
		t.Fatalf("AtoiArg on rendered value must succeed with the exact integer, got n=%d err=%v", n, err)
	}
	if strings.Contains(v, "e+0") {
		t.Fatalf("rendered value must not use exponent notation: %q", v)
	}
	if got := ArgString(map[string]any{"k": "  spaced  "}, "k"); got != "spaced" {
		t.Errorf("string trimming regressed: %q", got)
	}
	if got := ArgString(map[string]any{"k": nil}, "k"); got != "" {
		t.Errorf("nil must stay empty, got %q", got)
	}
}
