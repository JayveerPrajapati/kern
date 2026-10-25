package architecture

// TestFileAndFunctionLOCGates enforces the per-file and per-function LOC
// caps parsed from docs/file-caps.md — the per-file ledger that closes the
// gap the package caps in ARCHITECTURE.md leave open (a 3.4k-line engine.go
// fits inside a 12,900-line package cap and would otherwise grow forever).
// Same ratchet rule as the package ledger: crossing a cap forces
// decomposition, never a cap raise; caps are set ABOVE the measured floor
// (baseline × ~1.2, rounded up) so the gate binds future growth.

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const fileCapsDoc = "docs/file-caps.md"

func TestFileAndFunctionLOCGates(t *testing.T) {
	root := repoRoot(t)
	b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(fileCapsDoc)))
	if err != nil {
		t.Fatalf("read %s: %v", fileCapsDoc, err)
	}

	type capRow struct{ base, cap int }
	fileCaps := map[string]capRow{} // scope -> cap (file paths)
	funcCap := 0                    // the single global function/method cap
	rows := 0
	for _, ln := range strings.Split(string(b), "\n") {
		ln = strings.TrimSpace(ln)
		if ln == "" || !strings.HasPrefix(ln, "| `") {
			continue
		}
		cells := strings.Split(ln, "|")
		if len(cells) < 4 {
			continue
		}
		scope := strings.Trim(strings.TrimSpace(cells[1]), "`")
		base, errA := strconv.Atoi(strings.TrimSpace(cells[2]))
		capV, errB := strconv.Atoi(strings.TrimSpace(cells[3]))
		if errA != nil || errB != nil {
			t.Fatalf("bad cap row %q", ln)
		}
		rows++
		if strings.HasPrefix(scope, "function") {
			if funcCap != 0 {
				t.Fatalf("%s declares more than one function-cap row — exactly one is allowed", fileCapsDoc)
			}
			funcCap = capV
		} else {
			fileCaps[scope] = capRow{base: base, cap: capV}
		}
	}
	if rows < 10 {
		t.Fatalf("%s parsed only %d rows — table format broken?", fileCapsDoc, rows)
	}
	if funcCap == 0 {
		t.Fatalf("%s must declare a global function-cap row", fileCapsDoc)
	}

	// 1) Per-file caps: raw line count (newlines+1 — the package ledger's own
	// counting rule) must stay at or below the cap.
	for file, r := range fileCaps {
		src, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(file)))
		if err != nil {
			t.Errorf("read %s: %v", file, err)
			continue
		}
		lines := countLines(src)
		if lines > r.cap {
			t.Errorf("%s: %d lines exceeds file cap %d — split the file, never raise the cap", file, lines, r.cap)
		}
	}

	// 2) Global function cap: every function/method in every non-vendored,
	// non-test .go file must stay at or below the cap. Vendored/foreign
	// trees are skipped with the same skipDirs the package ledger uses.
	err = filepath.WalkDir(root, func(p string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if p != root && skipDirs[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), ".go") || strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		fset := token.NewFileSet()
		f, err := parser.ParseFile(fset, p, nil, parser.SkipObjectResolution)
		if err != nil {
			t.Errorf("parse %s: %v", p, err)
			return nil
		}
		for _, decl := range f.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok {
				continue
			}
			loc := fset.Position(fd.End()).Line - fset.Position(fd.Pos()).Line + 1
			if loc > funcCap {
				t.Errorf("%s: function %s is %d lines, exceeding the global function cap %d — decompose it, never raise the cap",
					p, fd.Name.Name, loc, funcCap)
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk: %v", err)
	}
}

func countLines(b []byte) int {
	if len(b) == 0 {
		return 0
	}
	return strings.Count(string(b), "\n") + 1
}
