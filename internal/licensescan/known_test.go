package licensescan

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestEscapeModulePath(t *testing.T) {
	if got, want := EscapeModulePath("github.com/KyleBanks/depth"), "github.com/!kyle!banks/depth"; got != want {
		t.Fatalf("EscapeModulePath = %q, want %q", got, want)
	}
}

func TestReadModuleLicenseFromCache(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	dir := filepath.Join(cache, "github.com", "!kyle!banks", "depth@v1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte("MIT License\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	data, ok := ReadModuleLicense("github.com/KyleBanks/depth")
	if !ok || !strings.Contains(string(data), "MIT License") {
		t.Fatalf("ReadModuleLicense = %q, %v", data, ok)
	}
	if _, ok := ReadModuleLicense("github.com/not/cached"); ok {
		t.Fatal("ReadModuleLicense invented a license for an uncached module")
	}
}

func TestKnown(t *testing.T) {
	cases := map[string]string{
		"github.com/stretchr/testify/assert":            "MIT",
		"golang.org/x/sys":                              "BSD-3-Clause",
		"github.com/KyleBanks/depth":                    "MIT",
		"gopkg.in/xmlpath.v2":                           "LGPL-3.0",
		"github.com/tree-sitter/tree-sitter-javascript": "MIT",
		"example.com/some/module":                       "",
	}
	for path, want := range cases {
		if got := Known(path); got != want {
			t.Errorf("Known(%q) = %q, want %q", path, got, want)
		}
	}
}
