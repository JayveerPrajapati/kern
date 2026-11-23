package architecture

import (
	"go/format"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestGoFmtClean pins a release-gate precondition: .github/workflows/test.yml
// fails the release when `gofmt -l .` is non-empty. Every Go source under
// internal/, cmd/ and evaluate/ must be go/format-stable. testdata fixtures,
// vendor trees, node_modules and dot-directories are skipped: they are not
// shipped sources and may intentionally hold odd formatting.
func TestGoFmtClean(t *testing.T) {
	root := repoRoot(t)
	var bad []string
	for _, sub := range []string{"internal", "cmd", "evaluate"} {
		base := filepath.Join(root, sub)
		err := filepath.WalkDir(base, func(p string, d os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				if p == base {
					return nil
				}
				name := d.Name()
				if name == "testdata" || name == "vendor" || name == "node_modules" || strings.HasPrefix(name, ".") {
					return filepath.SkipDir
				}
				return nil
			}
			if !strings.HasSuffix(d.Name(), ".go") {
				return nil
			}
			src, err := os.ReadFile(p)
			if err != nil {
				return err
			}
			rel, _ := filepath.Rel(root, p)
			out, err := format.Source(src)
			if err != nil {
				bad = append(bad, rel+": "+err.Error())
				return nil
			}
			if string(out) != string(src) {
				bad = append(bad, rel)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	if len(bad) > 0 {
		t.Fatalf("gofmt drift in %d file(s) — run `gofmt -w` on them:\n%s", len(bad), strings.Join(bad, "\n"))
	}
}
