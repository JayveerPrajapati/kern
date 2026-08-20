package verification

import (
	"bufio"
	"bytes"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// moduleDeps is the outcome of verifying a Go module's dependencies. It is
// deterministic, stdlib-only and runs fully in-process (no `go list` subprocess
// and no network).
type moduleDeps struct {
	// ok is false when the check could not run (e.g. no readable go.mod). The
	// caller treats that as fail-closed: it must not fabricate a PASS.
	ok bool
	// findings lists concrete anomalies: missing modules required by an import,
	// or a module required more than once (version duplication).
	findings []string
}

// checkModuleDeps verifies the module's real dependencies by parsing go.mod and
// the imports of every source file:
//
//   - every external (non-stdlib) import must be covered by a module in go.mod
//     (a missing module is an anomaly);
//   - no module path may be declared more than once in go.mod (duplication).
//
// It never shells out and never touches the network, so it is deterministic and
// fast on small fixtures.
func checkModuleDeps(root string) *moduleDeps {
	gomod := filepath.Join(root, "go.mod")
	data, err := os.ReadFile(gomod)
	if err != nil {
		return &moduleDeps{ok: false, findings: []string{
			"dependency check could not run: " + err.Error(),
		}}
	}

	modPath := modulePath(data)
	requires := parseRequires(gomod)
	imports := collectImports(root, modPath)

	var findings []string
	findings = append(findings, missingModules(imports, requires, modPath)...)
	findings = append(findings, duplicateRequires(requires)...)
	sort.Strings(findings)
	return &moduleDeps{ok: true, findings: findings}
}

// modulePath extracts the module path from a go.mod body.
func modulePath(data []byte) string {
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if rest, ok := strings.CutPrefix(line, "module "); ok {
			return strings.TrimSpace(rest)
		}
	}
	return ""
}

// parseRequires returns the ordered list of module paths declared in go.mod,
// handling both the single-line and block `require` forms.
func parseRequires(gomod string) []string {
	f, err := os.Open(gomod)
	if err != nil {
		return nil
	}
	defer f.Close()

	var mods []string
	inBlock := false
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		switch {
		case line == "require (":
			inBlock = true
			continue
		case inBlock && line == ")":
			inBlock = false
			continue
		case inBlock && line != "" && !strings.HasPrefix(line, "//"):
			if m, _, ok := splitModuleLine(line); ok {
				mods = append(mods, m)
			}
			continue
		}
		if rest, ok := strings.CutPrefix(line, "require "); ok && !strings.HasPrefix(rest, "(") {
			if m, _, ok := splitModuleLine(rest); ok {
				mods = append(mods, m)
			}
		}
	}
	return mods
}

// splitModuleLine splits a "module [version ...]" line into (modulePath, ok).
func splitModuleLine(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "//") {
		return "", "", false
	}
	fields := strings.Fields(line)
	if len(fields) < 1 {
		return "", "", false
	}
	return fields[0], "", true
}

// collectImports parses every Go source file under root and returns its imports
// in sorted order.
func collectImports(root, modPath string) []string {
	var out []string
	seen := map[string]bool{}
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			name := d.Name()
			if name == "vendor" || name == "testdata" || name == ".git" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) != ".go" {
			return nil
		}
		fset := token.NewFileSet()
		af, perr := parser.ParseFile(fset, path, nil, parser.ImportsOnly)
		if perr != nil {
			return nil
		}
		for _, imp := range af.Imports {
			if imp.Path == nil {
				continue
			}
			importPath := strings.Trim(imp.Path.Value, "\"`")
			if importPath == "" || seen[importPath] {
				continue
			}
			seen[importPath] = true
			out = append(out, importPath)
		}
		return nil
	})
	sort.Strings(out)
	return out
}

// missingModules returns a finding for every external import that no required
// module resolves. Stdlib and the module's own local packages are exempt.
func missingModules(imports, requires []string, modPath string) []string {
	var findings []string
	for _, imp := range imports {
		if isStdlib(imp) {
			continue
		}
		if modPath != "" && (imp == modPath || strings.HasPrefix(imp, modPath+"/")) {
			continue // local package within the module
		}
		if moduleResolves(imp, requires) {
			continue
		}
		findings = append(findings, "missing module for import "+imp)
	}
	return findings
}

// moduleResolves reports whether import path is equal to, or a child of, a
// required module path, matching Go's module resolution rule.
func moduleResolves(importPath string, requires []string) bool {
	for _, r := range requires {
		if importPath == r || strings.HasPrefix(importPath, r+"/") {
			return true
		}
	}
	return false
}

// duplicateRequires reports module paths declared more than once.
func duplicateRequires(requires []string) []string {
	counts := map[string]int{}
	for _, r := range requires {
		counts[r]++
	}
	var out []string
	for _, r := range requires {
		if counts[r] > 1 {
			out = append(out, "duplicate require for module "+r)
		}
	}
	return out
}

// isStdlib reports whether an import is part of the Go standard library (its
// first path segment contains no dot, e.g. "fmt", "net/http").
func isStdlib(importPath string) bool {
	first := importPath
	if i := strings.IndexByte(importPath, '/'); i >= 0 {
		first = importPath[:i]
	}
	return !strings.Contains(first, ".")
}
