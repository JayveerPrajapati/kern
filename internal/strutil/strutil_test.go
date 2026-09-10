package strutil

import (
	"math"
	"testing"
)

func TestPct(t *testing.T) {
	tests := []struct {
		name   string
		before int
		after  int
		want   float64
		eps    float64
	}{
		{name: "one quarter saved", before: 4, after: 3, want: 25},
		{name: "three tenths saved", before: 10, after: 7, want: 30},
		{name: "zero before", before: 0, after: 5, want: 0},
		{name: "negative before", before: -3, after: 1, want: 0},
		{name: "after zero", before: 5, after: 0, want: 100},
		{name: "no change", before: 5, after: 5, want: 0},
		{name: "improvement exceeds before is negative", before: 1, after: 4, want: -300},
		{name: "repeating decimal", before: 3, after: 1, want: 66.66666666666667, eps: 1e-9},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := Pct(tt.before, tt.after)
			eps := tt.eps
			if eps == 0 {
				eps = 1e-9
			}
			if math.Abs(got-tt.want) > eps {
				t.Errorf("Pct(%d, %d) = %v, want %v", tt.before, tt.after, got, tt.want)
			}
		})
	}
}

func TestSlug(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{name: "words", input: "Hello World", want: "hello-world"},
		{name: "double space collapses", input: "a  b", want: "a-b"},
		{name: "mixed case and punctuation", input: "UPPER_case!", want: "upper-case"},
		{name: "surrounding whitespace trimmed", input: "  spaced  ", want: "spaced"},
		{name: "alphanumeric unchanged", input: "abc123", want: "abc123"},
		{name: "double dash collapses", input: "a--b", want: "a-b"},
		{name: "already dashed", input: "a-b-c", want: "a-b-c"},
		{name: "digits and dash", input: "1-2", want: "1-2"},
		{name: "empty falls back", input: "", want: "doc"},
		{name: "no usable characters falls back", input: "!!!", want: "doc"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := Slug(tt.input); got != tt.want {
				t.Errorf("Slug(%q) = %q, want %q", tt.input, got, tt.want)
			}
		})
	}
}
