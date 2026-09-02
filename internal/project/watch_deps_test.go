package project

import (
	"testing"
	"time"
)

func TestRelatedFile(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"foo.go", "foo_test.go", true},
		{"foo.go", "foo_spec.rb", true},
		{"a/x.go", "a/y.go", true}, // same directory
		{"a/x.go", "b/x.go", false},
		{"x.go", "y.go", false},
		{"foo_test.go", "foo.go", true},
		{"pkg/foo.go", "pkg/foo_test.go", true}, // same stem, test sibling
		{"pkg/foo.go", "other/foo.go", false},   // same name, different dir
		{"a/x.go", "a/x_test.go", true},         // same stem, test sibling
		{"foo.go", "bar.go", false},             // same dir at root, unrelated names
		{"internal/p/w.go", "internal/p/w_test.go", true},
	}
	for _, tc := range cases {
		if got := relatedFile(tc.a, tc.b); got != tc.want {
			t.Errorf("relatedFile(%q, %q) = %v, want %v", tc.a, tc.b, got, tc.want)
		}
	}
}

func TestShouldExtendDependency(t *testing.T) {
	now := time.Now()
	window := time.Second
	cases := []struct {
		name   string
		recent map[string]time.Time
		want   bool
	}{
		{
			name: "both fresh and related",
			recent: map[string]time.Time{
				"foo.go":      now.Add(-10 * time.Millisecond),
				"foo_test.go": now.Add(-10 * time.Millisecond),
			},
			want: true,
		},
		{
			name: "one fresh one stale, related",
			recent: map[string]time.Time{
				"foo.go":      now.Add(-10 * time.Millisecond),
				"foo_test.go": now.Add(-2 * time.Minute),
			},
			want: true,
		},
		{
			name: "single entry",
			recent: map[string]time.Time{
				"foo.go": now.Add(-10 * time.Millisecond),
			},
			want: false,
		},
		{
			name: "both fresh but not related",
			recent: map[string]time.Time{
				"x.go": now.Add(-10 * time.Millisecond),
				"y.go": now.Add(-10 * time.Millisecond),
			},
			want: false,
		},
		{
			name:   "empty",
			recent: map[string]time.Time{},
			want:   false,
		},
	}
	for _, tc := range cases {
		if got := shouldExtendDependency(tc.recent, now, window); got != tc.want {
			t.Errorf("%s: shouldExtendDependency = %v, want %v", tc.name, got, tc.want)
		}
	}
}
