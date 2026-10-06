package verification

import (
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/bpcli/mcp"
)

// ChangedTestPackages derives the Go packages whose files have uncommitted
// changes (staged, unstaged or untracked) vs HEAD, for the changed-scope test
// tier (`kern verify --fast` and MCP kern_verify fast=true). Non-Go files are
// ignored (they do not run in the test step); renames follow the new path. An
// empty result means "no changed Go packages" and leaves the default scope
// alone. It was moved here from cmd/kern (where it was changedTestPackages) so
// the CLI and the MCP high-level surface share one canonical derivation.
func ChangedTestPackages(root string) []string {
	out, err := mcp.GitOutput(root, "status", "--porcelain", "-uall")
	if err != nil {
		return nil
	}
	seen := map[string]bool{}
	for _, line := range strings.Split(out, "\n") {
		if len(line) < 4 {
			continue
		}
		path := strings.TrimSpace(line[3:])
		// strip rename "old -> new": the new path is the one that exists
		if i := strings.Index(path, " -> "); i >= 0 {
			path = path[i+4:]
		}
		if !strings.HasSuffix(path, ".go") {
			continue
		}
		dir := filepath.Dir(filepath.ToSlash(path))
		if dir == "." {
			dir = "" // root-package files
		}
		pkg := "./" + dir
		if !seen[pkg] {
			seen[pkg] = true
		}
	}
	pkgs := make([]string, 0, len(seen))
	for pkg := range seen {
		pkgs = append(pkgs, pkg)
	}
	sort.Strings(pkgs)
	return pkgs
}
