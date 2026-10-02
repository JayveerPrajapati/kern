package loop

import (
	"path/filepath"
	"strings"
)

// changedGoPackages maps a worktree diff's changed .go files to their Go
// package patterns (./<dir>, or "." for root-package files), for scoped
// verification: a one-file task pays only its own package's tests instead of
// the whole-module suite. A git unified diff lists each touched file as an
// "+++ b/<path>" header line; non-Go files are ignored (the build check
// covers them), and duplicates collapse to one pattern per package.
//
// Empty or nil input returns nil, which the verification engine interprets
// as "no scope" (./...) — that is exactly the right fallback for read-only
// loops (L0/L1 make no changes) and diffs with no Go files.
func changedGoPackages(diff string) []string {
	seen := map[string]bool{}
	var pkgs []string
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+++ b/") {
			continue
		}
		path := strings.TrimPrefix(line, "+++ b/")
		if i := strings.IndexByte(path, '\t'); i >= 0 {
			path = path[:i] // git appends a tab + timestamp
		}
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		dir := filepath.Dir(path)
		if dir == "/" || dir == "" {
			continue
		}
		pkg := "./" + dir
		if dir == "." {
			pkg = "."
		}
		if !seen[pkg] {
			seen[pkg] = true
			pkgs = append(pkgs, pkg)
		}
	}
	return pkgs
}
