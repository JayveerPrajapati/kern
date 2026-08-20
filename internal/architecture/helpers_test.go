package architecture

import (
	"os"
	"path/filepath"
	"testing"
)

// scaffoldModule writes a temp directory containing a tiny Go module. The map
// keys are file paths relative to the module root (e.g. "web/web.go"). The
// default module path is example.com/fixture.
func scaffoldModule(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	all := map[string]string{
		"go.mod": "module example.com/fixture\n\ngo 1.23\n",
	}
	for k, v := range files {
		all[k] = v
	}
	for name, content := range all {
		p := filepath.Join(dir, name)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatalf("mkdir %s: %v", p, err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatalf("write %s: %v", p, err)
		}
	}
	return dir
}

// writeConfig writes a governance config under the module's .kern directory.
func writeConfig(t *testing.T, root, name, content string) {
	t.Helper()
	dir := filepath.Join(root, ".kern")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir .kern: %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// webToDBModule returns a two-package module where package web calls into db.
func webToDBModule() map[string]string {
	return map[string]string{
		"web/web.go": "package web\n\nimport \"example.com/fixture/db\"\n\nfunc Do() { db.Query() }\n",
		"db/db.go":   "package db\n\nfunc Query() {}\n",
	}
}
