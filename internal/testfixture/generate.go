// Package testfixture provides deterministic repository fixtures for tests.
package testfixture

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
)

// RepoSpec controls the shape of a generated fixture repository.
type RepoSpec struct {
	Files          int
	SymbolsPerFile int
	DeepCallGraph  bool
	UnicodeNames   bool
	DirtyGit       bool
	BinaryFiles    bool
	Symlinks       bool
}

var generatedCounter uint64

// Generate creates a fresh, deterministic Go repository with the requested
// shape and returns its root path. It is intended for load and stress tests
// that need more symbols than the tiny default fixture provides.
func Generate(t *testing.T, spec RepoSpec) string {
	t.Helper()

	if spec.Files < 1 {
		spec.Files = 1
	}
	if spec.SymbolsPerFile < 1 {
		spec.SymbolsPerFile = 1
	}

	root := t.TempDir()
	sequence := atomic.AddUint64(&generatedCounter, 1)
	module := fmt.Sprintf("example.com/generated%d", sequence)

	writeGeneratedFile(t, root, "go.mod", "module "+module+"\n\ngo 1.23\n")
	writeGeneratedFile(t, root, "main.go", generatedMain(spec))

	totalSymbols := spec.Files * spec.SymbolsPerFile
	for fileIndex := 0; fileIndex < spec.Files; fileIndex++ {
		start := fileIndex * spec.SymbolsPerFile
		stop := start + spec.SymbolsPerFile
		name := fmt.Sprintf("file%03d.go", fileIndex)
		writeGeneratedFile(t, root, name, generatedFile(fileIndex, start, stop, totalSymbols, spec))
	}

	if spec.BinaryFiles {
		writeBinaryFile(t, root)
	}
	if spec.Symlinks {
		writeSymlink(t, root)
	}

	initGeneratedRepo(t, root, spec.DirtyGit)
	return root
}

func generatedMain(spec RepoSpec) string {
	first := generatedSymbolName(0, spec.UnicodeNames)
	return fmt.Sprintf(`package main

func main() {
	_ = %s()
}
`, first)
}

func generatedFile(fileIndex, start, stop, total int, spec RepoSpec) string {
	var b strings.Builder
	fmt.Fprintf(&b, "package main\n\n")
	for i := start; i < stop; i++ {
		name := generatedSymbolName(i, spec.UnicodeNames)
		if spec.DeepCallGraph && i+1 < total {
			next := generatedSymbolName(i+1, spec.UnicodeNames)
			fmt.Fprintf(&b, "func %s() int { return %s() }\n", name, next)
		} else {
			fmt.Fprintf(&b, "func %s() int { return %d }\n", name, i)
		}
	}
	_ = fileIndex
	return b.String()
}

func generatedSymbolName(index int, unicode bool) string {
	if unicode {
		return fmt.Sprintf("Şymbol%04d", index)
	}
	return fmt.Sprintf("Symbol%04d", index)
}

func writeGeneratedFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("generated fixture mkdir %s: %v", path, err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("generated fixture write %s: %v", path, err)
	}
}

func writeBinaryFile(t *testing.T, root string) {
	t.Helper()
	path := filepath.Join(root, "binary.dat")
	data := []byte{0x00, 0x01, 0x02, 0x03, 0xff, 0xfe}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatalf("generated fixture binary write: %v", err)
	}
}

func writeSymlink(t *testing.T, root string) {
	t.Helper()
	source := filepath.Join(root, "file000.go")
	target := filepath.Join(root, "symlink.go")
	if err := os.Symlink(source, target); err != nil {
		t.Fatalf("generated fixture symlink: %v", err)
	}
}

func initGeneratedRepo(t *testing.T, root string, dirty bool) {
	t.Helper()
	runGit(t, root, "init", "-q")
	runGit(t, root, "config", "user.name", "kern-test")
	runGit(t, root, "config", "user.email", "kern-test@example.com")
	runGit(t, root, "add", "-A")
	runGit(t, root, "-c", "core.hooksPath=", "commit", "-qm", "generated fixture")

	if dirty {
		path := filepath.Join(root, "dirty.go")
		if err := os.WriteFile(path, []byte("package main\n\nfunc Dirty() {}\n"), 0o644); err != nil {
			t.Fatalf("generated fixture dirty write: %v", err)
		}
	}
}

func runGit(t *testing.T, root string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("generated fixture git %v: %v\n%s", args, err, out)
	}
}
