package verification

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestVerifyLicenseReadsModuleCache pins the go.mod-only fallback: when no
// vendor tree exists, the dependency's own LICENSE is read from the local Go
// module cache instead of being reported as unknown.
func TestVerifyLicenseReadsModuleCache(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n\ngo 1.22\n\nrequire example.com/dep v1.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	dir := filepath.Join(cache, "example.com", "dep@v1.0.0")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	license := "Apache License\nVersion 2.0, January 2004\nhttp://www.apache.org/licenses/\n"
	if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(license), 0o644); err != nil {
		t.Fatal(err)
	}

	res := NewEngine(root).VerifyLicense()
	for _, entry := range res.Modules {
		if entry.Module == "example.com/dep" {
			if entry.License != "Apache-2.0" {
				t.Fatalf("dependency license = %q, want Apache-2.0 (entries: %+v)", entry.License, res.Modules)
			}
			return
		}
	}
	t.Fatalf("dependency missing from license result: %+v", res.Modules)
}

// TestVerifyLicenseModuleCacheTieGoesToNewest guards the deterministic cache
// choice: the newest cached version's LICENSE wins.
func TestVerifyLicenseModuleCacheTieGoesToNewest(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module example.com/app\n\ngo 1.22\n\nrequire example.com/dep v2.0.0\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cache := t.TempDir()
	t.Setenv("GOMODCACHE", cache)
	for version, license := range map[string]string{
		"v1.0.0": "MIT License\n",
		"v2.0.0": "Apache License\nVersion 2.0, January 2004\n",
	} {
		dir := filepath.Join(cache, "example.com", "dep@"+version)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "LICENSE"), []byte(license), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	res := NewEngine(root).VerifyLicense()
	for _, entry := range res.Modules {
		if entry.Module == "example.com/dep" && !strings.EqualFold(entry.License, "Apache-2.0") {
			t.Fatalf("dependency license = %q, want the newest cached version's Apache-2.0", entry.License)
		}
	}
}
