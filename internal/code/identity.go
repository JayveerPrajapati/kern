package code

import (
	"path/filepath"
	"strings"
)

// IsTestFile reports whether a relative path is a test file. It covers the
// common conventions: *_test.go, *.test.js/ts, *_spec.rb, test_*.py and files
// under test/tests/spec/__tests__ directories.
func IsTestFile(rel string) bool {
	lower := strings.ToLower(rel)
	base := filepath.Base(lower)
	if strings.HasSuffix(lower, "_test.go") {
		return true
	}
	for _, suffix := range []string{
		".test.ts", ".test.tsx", ".test.js", ".test.jsx", ".test.mjs", ".test.cjs",
		".test.py", ".test.rb", ".test.go",
		".spec.ts", ".spec.tsx", ".spec.js", ".spec.jsx", ".spec.rb", ".spec.py",
		"_test.py", "_test.rb", "_test.js", "_test.ts", "_spec.rb", "_spec.py",
	} {
		if strings.HasSuffix(lower, suffix) {
			return true
		}
	}
	if strings.HasPrefix(base, "test_") || strings.HasPrefix(base, "test-") {
		return true
	}
	for _, dir := range []string{"/test/", "/tests/", "/spec/", "/__tests__/"} {
		if strings.Contains("/"+lower+"/", dir) {
			return true
		}
	}
	return false
}

// IsFixtureFile reports whether a relative path lives under a testdata/
// directory segment — Go's canonical location for test fixtures and demo
// code. Fixtures are not production source, so architecture enforcement must
// not flag (or silently skip-check) their crossings.
func IsFixtureFile(rel string) bool {
	return strings.Contains("/"+filepath.ToSlash(rel)+"/", "/testdata/")
}
