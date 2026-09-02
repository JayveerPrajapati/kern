package index

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// parallelTestTree writes a deterministic multi-package fixture (Go files with
// cross-package imports and calls, plus a couple of foreign-language files) so
// the parallel and serial builds exercise every merge path: Symbols, Calls,
// Inherits, Pkg with import merging, ImportsByFile and GeneratedFiles.
func parallelTestTree(t *testing.T) string {
	t.Helper()
	files := map[string]string{}
	for p := 0; p < 4; p++ {
		pkg := fmt.Sprintf("pkg%d", p)
		imports := ""
		if p > 0 {
			imports = fmt.Sprintf("\nimport \"pkg%d\"\n", p-1)
		}
		for f := 0; f < 5; f++ {
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
			files[filepath.Join(pkg, fmt.Sprintf("file%d.go", f))] = body
		}
	}
	files["scripts/util.py"] = "def helper():\n    return 1\n\n\ndef run():\n    return helper()\n"
	files["scripts/app.js"] = "function helper() { return 1; }\nfunction run() { return helper(); }\n"
	return writeTree(t, files)
}

// assertIndexesByteIdentical zeroes the wall-clock fields that legitimately
// differ between two builds (UpdatedAt, and the identity's BuiltAt stamp,
// which buildIdentity derives from UpdatedAt) and then requires the marshaled
// indexes to match byte for byte.
func assertIndexesByteIdentical(t *testing.T, a, b *Index) {
	t.Helper()
	a.UpdatedAt = time.Time{}
	b.UpdatedAt = time.Time{}
	if a.Identity != nil {
		a.Identity.BuiltAt = time.Time{}
	}
	if b.Identity != nil {
		b.Identity.BuiltAt = time.Time{}
	}
	aj, err := json.Marshal(a)
	if err != nil {
		t.Fatal(err)
	}
	bj, err := json.Marshal(b)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(aj, bj) {
		t.Fatalf("indexes diverge:\nserial:   %s\nparallel: %s", aj, bj)
	}
}

// TestBuildParallelMatchesSerial: the parallel build must produce an index
// byte-identical to the serial build over the same tree.
func TestBuildParallelMatchesSerial(t *testing.T) {
	dir := parallelTestTree(t)
	serial, err := buildSerial(dir, &buildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := buildParallel(dir, &buildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	if len(serial.Symbols) == 0 {
		t.Fatal("fixture produced no symbols")
	}
	if len(serial.Symbols) != len(parallel.Symbols) {
		t.Fatalf("symbol count mismatch: serial=%d parallel=%d", len(serial.Symbols), len(parallel.Symbols))
	}
	if len(serial.FileHashes) != len(parallel.FileHashes) {
		t.Fatalf("file hash count mismatch: serial=%d parallel=%d", len(serial.FileHashes), len(parallel.FileHashes))
	}
	assertIndexesByteIdentical(t, serial, parallel)
}

// TestBuildParallelSkipSemantics: files the serial build skips (unreadable via
// a broken symlink, oversized) and files that fail to parse must be handled
// identically by the parallel build, including the staleness invariant that an
// unparseable file's hash is still recorded in FileHashes. The unreadable
// symlink is created after every other file, so it is the newest on disk —
// if the parallel build folded its mtime into MaxMtime the way the serial
// build does not, the byte-identity and MaxMtime assertions would fail.
func TestBuildParallelSkipSemantics(t *testing.T) {
	dir := parallelTestTree(t)
	// An unparseable Go file: must still be hashed into FileHashes (staleness
	// invariant) but contribute no symbols, in both builds.
	if err := os.WriteFile(filepath.Join(dir, "broken.go"), []byte("package broken\nfunc (\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// A broken symlink: unreadable, must be skipped by both builds.
	if err := os.Symlink(filepath.Join(dir, "does-not-exist.go"), filepath.Join(dir, "unreadable.go")); err != nil {
		t.Fatal(err)
	}
	// An oversized file (larger than maxFileBytes): skipped before reading by
	// both builds.
	huge := filepath.Join(dir, "huge.go")
	if err := os.WriteFile(huge, []byte(strings.Repeat("x", maxFileBytes+1)), 0o644); err != nil {
		t.Fatal(err)
	}
	serial, err := buildSerial(dir, &buildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	parallel, err := buildParallel(dir, &buildConfig{})
	if err != nil {
		t.Fatal(err)
	}

	// Staleness invariant: the unparseable file's hash is recorded in both.
	if _, ok := serial.FileHashes["broken.go"]; !ok {
		t.Fatal("serial build must record the hash of an unparseable file")
	}
	if _, ok := parallel.FileHashes["broken.go"]; !ok {
		t.Fatal("parallel build must record the hash of an unparseable file")
	}
	// Neither build may record the unreadable or oversized files.
	for _, name := range []string{"unreadable.go", "huge.go"} {
		if _, ok := serial.FileHashes[name]; ok {
			t.Fatalf("serial build must not hash %s", name)
		}
		if _, ok := parallel.FileHashes[name]; ok {
			t.Fatalf("parallel build must not hash %s", name)
		}
	}
	if len(serial.Symbols) != len(parallel.Symbols) {
		t.Fatalf("symbol count mismatch: serial=%d parallel=%d", len(serial.Symbols), len(parallel.Symbols))
	}
	// MaxMtime parity: serial records MaxMtime only after ReadFile and the
	// isIndexable check, so the unreadable symlink and oversized file are
	// excluded — the parallel build must exclude them too (and the unparseable
	// broken.go, which IS indexable, must be included in both).
	if serial.MaxMtime != parallel.MaxMtime {
		t.Fatalf("MaxMtime mismatch: serial=%d parallel=%d", serial.MaxMtime, parallel.MaxMtime)
	}
	assertIndexesByteIdentical(t, serial, parallel)
}

// TestBuildParallelDeterministicRepeat: two parallel builds over the same tree
// must be byte-identical (modulo wall-clock timestamps).
func TestBuildParallelDeterministicRepeat(t *testing.T) {
	dir := parallelTestTree(t)
	a, err := buildParallel(dir, &buildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := buildParallel(dir, &buildConfig{})
	if err != nil {
		t.Fatal(err)
	}
	assertIndexesByteIdentical(t, a, b)
}
