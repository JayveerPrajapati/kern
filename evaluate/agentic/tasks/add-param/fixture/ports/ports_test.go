package ports

import "testing"

func TestParseMax(t *testing.T) {
	cases := []struct {
		in   string
		max  int
		want int
		ok   bool
	}{
		{"8080", 65535, 8080, true},
		{"70000", 65535, 0, false},
		{"80", 1023, 80, true},
		{"8080", 1023, 0, false},
		{"0", 65535, 0, false},
		{"abc", 65535, 0, false},
	}
	for _, c := range cases {
		got, err := Parse(c.in, c.max)
		if (err == nil) != c.ok || got != c.want {
			t.Errorf("Parse(%q, %d) = %d, %v; want %d, ok=%v", c.in, c.max, got, err, c.want, c.ok)
		}
	}
}
