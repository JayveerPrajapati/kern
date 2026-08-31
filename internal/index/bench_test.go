package index

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// benchTree writes a deterministic multi-package fixture and returns its root.
// pkg0 is the hub: every later package imports the one before it and each
// file's helper calls back into the previous package, so Build resolves both
// intra- and inter-package call edges. The tree is written once, outside the
// timed loop, so the benchmark measures the build itself.
func benchTree(b *testing.B, pkgCount, filesPerPkg int) string {
	b.Helper()
	dir := b.TempDir()
	for p := 0; p < pkgCount; p++ {
		pkg := fmt.Sprintf("pkg%d", p)
		if err := os.MkdirAll(filepath.Join(dir, pkg), 0o755); err != nil {
			b.Fatal(err)
		}
		imports := ""
		if p > 0 {
			imports = fmt.Sprintf("\nimport \"pkg%d\"\n", p-1)
		}
		for f := 0; f < filesPerPkg; f++ {
			callee := `"x"`
			if p > 0 {
				callee = fmt.Sprintf("pkg%d.Func%d()", p-1, f)
			}
			body := fmt.Sprintf(`package %s
%s
func Func%d() string {
	return Helper%d()
}

func Helper%d() string {
	return %s
}

type T%d struct{ V int }

func (t T%d) Method%d() int { return t.V + %d }
`, pkg, imports, f, f, f, callee, f, f, f, f)
			rel := filepath.Join(pkg, fmt.Sprintf("file%d.go", f))
			if err := os.WriteFile(filepath.Join(dir, rel), []byte(body), 0o644); err != nil {
				b.Fatal(err)
			}
		}
	}
	return dir
}

// BenchmarkIndexBuild measures a full index build over a small deterministic
// tree (3 packages, 12 files) with cross-package call edges.
func BenchmarkIndexBuild(b *testing.B) {
	dir := benchTree(b, 3, 4)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix, err := Build(dir)
		if err != nil {
			b.Fatal(err)
		}
		if len(ix.Symbols) == 0 {
			b.Fatal("Build produced no symbols")
		}
	}
}

// BenchmarkIndexBuildLarge measures a full index build over a larger
// synthetic tree (6 packages, 60 files). The fixture is generated
// programmatically, so the larger variant is as cheap to set up as the small
// one.
func BenchmarkIndexBuildLarge(b *testing.B) {
	dir := benchTree(b, 6, 10)
	b.ReportAllocs()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		ix, err := Build(dir)
		if err != nil {
			b.Fatal(err)
		}
		if len(ix.Symbols) == 0 {
			b.Fatal("Build produced no symbols")
		}
	}
}
