package index

import (
	"fmt"
	"path/filepath"
	"testing"
)

// parallel_stream_test.go — coverage for buildParallel's pool path. The three
// pinned tests in parallel_test.go use a 22-file fixture, which stays below
// the adaptive parallelMin threshold (floor 256) on every machine, so they
// exercise the small-tree serial fast path only. This file's test builds a
// fixture sized above this machine's parallelMin (and above the 1024 reorder
// window where possible) so the streamed pool path — bounded job channel,
// feeder-side window enforcement, ordered merge — is what runs.

// parallelStreamTree writes a deterministic multi-package Go fixture with at
// least target accepted files, mirroring parallelTestTree's cross-package
// structure so every merge path (Symbols, Calls, Inherits, Pkg imports,
// ImportsByFile) is exercised.
func parallelStreamTree(t *testing.T, target int) string {
	t.Helper()
	const perPkg = 50
	pkgs := (target + perPkg - 1) / perPkg
	files := map[string]string{}
	for p := 0; p < pkgs; p++ {
		pkg := fmt.Sprintf("pkg%d", p)
		imports := ""
		if p > 0 {
			imports = fmt.Sprintf("\nimport \"pkg%d\"\n", p-1)
		}
		for f := 0; f < perPkg; f++ {
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
`, pkg, imports, f, f, f, callee)
			files[filepath.Join(pkg, fmt.Sprintf("file%d.go", f))] = body
		}
	}
	files["scripts/util.py"] = "def helper():\n    return 1\n\n\ndef run():\n    return helper()\n"
	return writeTree(t, files)
}

// TestBuildParallelPoolPathMatchesSerial: the streamed pool path must produce
// an index byte-identical to the serial build, and two pool-path builds must
// be byte-identical to each other (the determinism the freshness/identity
// proofs rely on).
func TestBuildParallelPoolPathMatchesSerial(t *testing.T) {
	tuns := resolveTunables(&buildConfig{}, resolveResources())
	target := tuns.parallelMin + 128
	if target < reorderWindow+64 {
		target = reorderWindow + 64
	}
	dir := parallelStreamTree(t, target)

	serial, err := buildSerial(dir, &buildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	a, err := buildParallel(dir, &buildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildParallel(dir, &buildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(serial.Symbols) == 0 {
		t.Fatal("fixture produced no symbols")
	}
	if len(serial.Symbols) != len(a.Symbols) {
		t.Fatalf("symbol count mismatch: serial=%d parallel=%d", len(serial.Symbols), len(a.Symbols))
	}
	assertIndexesByteIdentical(t, serial, a)
	assertIndexesByteIdentical(t, a, b)
}
