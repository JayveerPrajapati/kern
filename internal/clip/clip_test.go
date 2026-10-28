package clip

import (
	"strings"
	"testing"
)

func TestClipReflect(t *testing.T) {
	t.Parallel()
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"plain path unchanged", "/Users/alice/proj", 160, "/Users/alice/proj"},
		{"empty string", "", 160, ""},
		{"control runes stripped", "/tmp/\x1b[2J\x07evil\r\n/path", 160, "/tmp/[2Jevil/path"},
		{"DEL rune stripped", "/tmp/e\x7fvil", 160, "/tmp/evil"},
		{"clip at budget with ellipsis", "abcdefghij", 4, "abcd…"},
		{"exact budget still marks the clip", "abcd", 4, "abcd…"},
		{"long adversarial path bounded", strings.Repeat("x", 10_000), 160, strings.Repeat("x", 160) + "…"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ClipReflect(tt.in, tt.max); got != tt.want {
				t.Errorf("ClipReflect(%q, %d) = %q, want %q", tt.in, tt.max, got, tt.want)
			}
		})
	}
}

// TestClipReflectNoControlRunes is the LOW-8 probe as a unit pin: for a
// hostile root full of every C0 control rune, no control rune survives and
// the output is bounded.
func TestClipReflectNoControlRunes(t *testing.T) {
	t.Parallel()
	hostile := "/tmp/\x00\x01\x02\x03\x04\x05\x06\x07\x08\x09\x0b\x0c\x0e\x0f\x10\x11\x12\x13\x14\x15\x16\x17\x18\x19\x1a\x1b\x1c\x1d\x1e\x1froot"
	got := ClipReflect(hostile, 160)
	for _, r := range got {
		if r < 0x20 || r == 0x7f {
			t.Fatalf("control rune %q survived ClipReflect: %q", r, got)
		}
	}
	if len(got) > 164 { // budget + ellipsis + multi-byte slack
		t.Errorf("ClipReflect output unbounded: %d bytes", len(got))
	}
}
