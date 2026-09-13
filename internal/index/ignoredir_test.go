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
		{name: ".venv", want: true},
		{name: "venv", want: true},
		{name: "__pypackages__", want: true},
		{name: ".opencode", want: true},
		{name: ".agents", want: true},
		{name: ".vscode", want: true},
		{name: ".continue", want: true},
		{name: ".windsurf", want: true},
		{name: "src", want: false},
		{name: "venvs", want: false},
		{name: "", want: false},
	}
	for _, tt := range tests {
		if got := IgnoredDir(tt.name); got != tt.want {
			t.Errorf("IgnoredDir(%q) = %v, want %v", tt.name, got, tt.want)
		}
	}
}
