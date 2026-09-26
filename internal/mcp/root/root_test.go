package root

import (
	"os"
	"path/filepath"
	"testing"
)

func TestResolveRoot(t *testing.T) {
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd: %v", err)
	}
	cwd = filepath.Clean(cwd)

	tests := []struct {
		name string
		in   string
		want string
	}{
		{"empty uses cwd", "", cwd},
		{"relative made absolute", "some/rel/path", filepath.Join(cwd, "some/rel/path")},
		{"absolute cleaned", filepath.Join(cwd, "sub", ".."), cwd},
		{"trailing slash cleaned", cwd + "/", cwd},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ResolveRoot(tt.in); got != tt.want {
				t.Errorf("ResolveRoot(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}
