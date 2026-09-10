package index

import "testing"

func TestIgnoredDir(t *testing.T) {
	tests := []struct {
		name string
		want bool
	}{
		{name: ".git", want: true},
		{name: ".kern", want: true},
		{name: "node_modules", want: true},
		{name: "vendor", want: true},
		{name: "dist", want: true},
		{name: "src", want: false},
		{name: "", want: false},
	}
	for _, tt := range tests {
		if got := IgnoredDir(tt.name); got != tt.want {
			t.Errorf("IgnoredDir(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
