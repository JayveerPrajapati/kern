package gates

import (
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// repoRoot returns the blueprint repository root (the directory containing
// go.mod), found by walking up from this package's directory.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	root := ""
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			root = dir
			break
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatalf("repository root (go.mod) not found walking up from %s", dir)
		}
		dir = parent
	}
	return root
}

// gateNumberFromFunc extracts the gate number from a gate test function name:
// "TestG17_JSONShape" -> 17. Returns -1 when the name does not carry a gate
// number.
func gateNumberFromFunc(name string) int {
	m := gateFuncRe.FindStringSubmatch(name)
	if m == nil {
		return -1
	}
	n := 0
	for _, d := range m[1] {
		n = n*10 + int(d-'0')
	}
	return n
}

var (
	// gateFuncRe matches `TestG<NN>_...` test function names.
	gateFuncRe = regexp.MustCompile(`^TestG([0-9]+)_`)
	// contractFuncRe matches G14's special `TestContract` naming family.
	contractFuncRe = regexp.MustCompile(`^TestContract`)
	// gitleaksFuncRe matches G3's primary-adapter test family. The gitleaks
	// adapter is wired first in buildCheckList, so its TestGitleaks* tests
	// are claimed by G3 (the TestContractGitleaks* ones stay with G14).
	gitleaksFuncRe = regexp.MustCompile(`^TestGitleaks`)
	// jscpdFuncRe matches G6's primary-adapter test family, claimed by G6.
	jscpdFuncRe = regexp.MustCompile(`^TestJSCPD`)
	// funcDeclRe finds a Go func declaration by name: `func <name>(`.
	funcDeclRe = regexp.MustCompile(`\bfunc\s+([A-Za-z0-9_]+)\s*\(`)
)

// skipDir reports whether a directory should be excluded from the repo walk
// (VCS metadata, build output, dependency caches, tool state).
func skipDir(name string) bool {
	if strings.HasPrefix(name, ".") {
		return true // .git, .blueprint, .kern, editor dirs, ...
	}
	switch name {
	case "bin", "vendor", "node_modules", "dist", "build":
		return true
	}
	return false
}

// collectTestFiles walks the repo and returns every *_test.go path (repo
// relative) plus its contents.
func collectTestFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() {
			if path != root && skipDir(d.Name()) {
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(d.Name(), "_test.go") {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[rel] = string(data)
		return nil
	})
	if err != nil {
		t.Fatalf("walk repo: %v", err)
	}
	return files
}

// TestRegistryShape pins the registry contract: exactly 30 gates, IDs G0-G29
// in order with no gaps or duplicates, and every field populated.
func TestRegistryShape(t *testing.T) {
	if len(Registry) != 30 {
		t.Fatalf("Registry has %d entries, want 30 (G0-G29)", len(Registry))
	}
	seen := make(map[string]bool, len(Registry))
	for i, g := range Registry {
		wantID := "G" + itoa(i)
		if g.ID != wantID {
			t.Errorf("Registry[%d].ID = %q, want %q (gates must be G0-G29 in order)", i, g.ID, wantID)
		}
		if seen[g.ID] {
			t.Errorf("duplicate gate ID %q", g.ID)
		}
		seen[g.ID] = true
		if g.Name == "" {
			t.Errorf("gate %s: Name is empty", g.ID)
		}
		if g.Verifies == "" {
			t.Errorf("gate %s: Verifies is empty", g.ID)
		}
		switch g.Enforcement {
		case "block", "warn", "skip", "info":
		default:
			t.Errorf("gate %s: Enforcement %q is not one of block/warn/skip/info", g.ID, g.Enforcement)
		}
		if g.Package == "" {
			t.Errorf("gate %s: Package is empty", g.ID)
		}
		if !strings.HasSuffix(g.TestFile, "_test.go") {
			t.Errorf("gate %s: TestFile %q does not end in _test.go", g.ID, g.TestFile)
		}
		if len(g.TestFuncs) == 0 {
			t.Errorf("gate %s: TestFuncs is empty (every gate must be backed by at least one test)", g.ID)
		}
	}
}

// itoa is a tiny integer-to-string helper (keeps the test free of strconv
// ceremony for gate numbers 0-29).
func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

// TestRegistryForwardMapping — registry -> tests. Every registry entry must
// point at a test file that exists on disk, and every listed test function
// must exist as a real `func <Name>(...)` declaration in a *_test.go file.
// A gate listed in the registry with no corresponding test is an orphan.
func TestRegistryForwardMapping(t *testing.T) {
	root := repoRoot(t)
	testFiles := collectTestFiles(t, root)

	for _, g := range Registry {
		abs := filepath.Join(root, g.TestFile)
		if _, err := os.Stat(abs); err != nil {
			t.Errorf("gate %s: TestFile %q does not exist on disk", g.ID, g.TestFile)
			continue
		}
		for _, fn := range g.TestFuncs {
			if fn == "" {
				t.Errorf("gate %s: empty entry in TestFuncs", g.ID)
				continue
			}
			// The function must be declared in the gate's own test file.
			if content, ok := testFiles[g.TestFile]; ok && funcDeclared(content, fn) {
				continue
			}
			// Fall back to searching every test file: several gates are
			// proven by functions spread across more than one file (e.g. G16
			// lives in policy/engine_test.go AND policy/loader_test.go). The
			// function must still exist somewhere as a test declaration.
			found := false
			for rel, content := range testFiles {
				if funcDeclared(content, fn) {
					found = true
					t.Logf("gate %s: %s declared in %s (extra file)", g.ID, fn, rel)
					break
				}
			}
			if !found {
				t.Errorf("gate %s: test function %q from TestFile %q is declared nowhere in the repo", g.ID, fn, g.TestFile)
			}
			// The function's embedded gate number must match its registry ID
			// (a TestG5_* function must not be listed under G6).
			if n := gateNumberFromFunc(fn); n >= 0 {
				if "G"+itoa(n) != g.ID {
					t.Errorf("gate %s: TestFuncs contains %q whose number is G%d (misattributed)", g.ID, fn, n)
				}
			}
		}
	}
}

// funcDeclared reports whether content declares `func <name>(`.
func funcDeclared(content, name string) bool {
	for _, m := range funcDeclRe.FindAllStringSubmatch(content, -1) {
		if m[1] == name {
			return true
		}
	}
	return false
}

// TestRegistryReverseMapping — tests -> registry. Every gate-named test in the
// repo (func TestG<NN>_... anywhere, plus G14's special func TestContract*
// family, and G3/G6's primary-adapter families TestGitleaks*/TestJSCPD*) must
// have a registry entry for its gate number. A test that exists for a gate
// with no registry entry is an orphan.
func TestRegistryReverseMapping(t *testing.T) {
	root := repoRoot(t)
	testFiles := collectTestFiles(t, root)

	registered := make(map[string]bool, len(Registry))
	for _, g := range Registry {
		registered[g.ID] = true
	}

	// gate number -> example func name, for the error message.
	seen := make(map[int]string)
	for _, content := range testFiles {
		for _, name := range declaredFuncs(content) {
			if contractFuncRe.MatchString(name) {
				// G14's naming family: TestContract* funcs belong to G14.
				// (Other contract families — TestContractGitleaks*,
				// TestContractJSCPD* — share the prefix and map to G14 as
				// well; the reverse check only needs the number to exist.)
				seen[14] = name
				continue
			}
			if gitleaksFuncRe.MatchString(name) {
				// G3's primary-adapter family (gitleaks).
				seen[3] = name
				continue
			}
			if jscpdFuncRe.MatchString(name) {
				// G6's primary-adapter family (jscpd).
				seen[6] = name
				continue
			}
			if n := gateNumberFromFunc(name); n >= 0 {
				if _, ok := seen[n]; !ok {
					seen[n] = name
				}
			}
		}
	}

	// Every gate number exercised by a test must have a registry entry.
	for n, example := range seen {
		id := "G" + itoa(n)
		if !registered[id] {
			t.Errorf("test %q exists for gate %s, but no registry entry claims %s (orphan test)", example, id, id)
		}
	}

	// The registry must not claim gate numbers that no test exercises: a
	// registered gate with no test at all is an orphan gate.
	for _, g := range Registry {
		if n := gateNumberFromFunc(g.TestFuncs[0]); n >= 0 {
			if _, ok := seen[n]; !ok {
				t.Errorf("gate %s is registered but no test in the repo exercises gate %d (orphan gate)", g.ID, n)
			}
		}
	}

	// G14's own contract functions must be declared in the registered file.
	g14, ok := registeredGate(t, "G14")
	if ok {
		content, exists := testFiles[g14.TestFile]
		if !exists {
			t.Errorf("gate G14: TestFile %q not collected", g14.TestFile)
		} else {
			for _, fn := range g14.TestFuncs {
				if !funcDeclared(content, fn) {
					t.Errorf("gate G14: %s not declared in %s", fn, g14.TestFile)
				}
			}
		}
	}
}

// registeredGate returns the registry entry for id.
func registeredGate(t *testing.T, id string) (Gate, bool) {
	t.Helper()
	for _, g := range Registry {
		if g.ID == id {
			return g, true
		}
	}
	return Gate{}, false
}

// declaredFuncs returns the names of every top-level `func <name>(` declared
// in content (test functions and helpers alike).
func declaredFuncs(content string) []string {
	var names []string
	for _, m := range funcDeclRe.FindAllStringSubmatch(content, -1) {
		names = append(names, m[1])
	}
	return names
}
