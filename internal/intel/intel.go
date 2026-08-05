// Package intel turns kern's AST index into a review-grade code intelligence
// engine: change impact (blast radius + risk), test-coverage gaps, hub/bridge
// hotspots, execution flows and community clustering. It is 100% dependency
// free — pure Go over the persisted index, no Tree-sitter, no database.
package intel

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// ChangedFiles returns the files changed in the working tree (staged +
// unstaged) relative to HEAD. Falls back to `git status --porcelain` when the
// repository has no commits yet.
func ChangedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "diff", "HEAD", "--name-only")
	out, err := cmd.Output()
	if err != nil {
		out, err = exec.Command("git", "-C", root, "status", "--porcelain").Output()
		if err != nil {
			return nil, &GitError{Op: "git diff HEAD --name-only", Err: err}
		}
		return parsePorcelain(string(out)), nil
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

// FilesForRange returns the files changed between from..to. An empty range
// means the working tree (ChangedFiles).
func FilesForRange(root, from, to string) ([]string, error) {
	if from == "" && to == "" {
		return ChangedFiles(root)
	}
	cmd := exec.Command("git", "-C", root, "diff", "--name-only", from+".."+to)
	out, err := cmd.Output()
	if err != nil {
		return nil, &GitError{Op: "git diff --name-only " + from + ".." + to, Err: err}
	}
	var files []string
	for _, l := range strings.Split(strings.TrimSpace(string(out)), "\n") {
		if l != "" {
			files = append(files, l)
		}
	}
	return files, nil
}

func parsePorcelain(out string) []string {
	var files []string
	for _, l := range strings.Split(out, "\n") {
		l = strings.TrimSpace(l)
		if len(l) < 4 {
			continue
		}
		name := l[3:]
		name = strings.TrimPrefix(name, `"`)
		name = strings.TrimSuffix(name, `"`)
		if name != "" {
			files = append(files, name)
		}
	}
	return files
}

// GitError is returned when a git subprocess fails.
type GitError struct {
	Op  string
	Err error
}

func (e *GitError) Error() string {
	return "git failed (" + e.Op + "): " + e.Err.Error()
}

// isTestFile reports whether a relative path is a test file. It covers the
// common conventions: *_test.go, *.test.js/ts, *_spec.rb, test_*.py and files
// under test/tests/spec/__tests__ directories.
func isTestFile(rel string) bool {
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

// isEntryPoint reports whether a symbol name is conventionally an entry point.
func isEntryPoint(name string) bool {
	switch name {
	case "main", "init", "run", "Run", "Main", "setup", "Setup", "start", "Start":
		return true
	}
	return false
}

// buildFileMap returns symbol -> source file for every indexed symbol.
func buildFileMap(ix *index.Index) map[string]string {
	m := map[string]string{}
	for _, s := range ix.Symbols {
		if s.File != "" {
			m[s.FullName()] = s.File
		}
	}
	return m
}

// dirOf returns the package-ish directory of a symbol's file ("" when unknown).
func dirOf(fileMap map[string]string, sym string) string {
	f, ok := fileMap[sym]
	if !ok {
		return ""
	}
	d := filepath.Dir(f)
	if d == "." {
		return ""
	}
	return d
}

// prodCallers returns the callers of sym that live outside test files.
// Unknown symbols (e.g. stdlib) are kept — they are not in this project's
// tests.
func prodCallers(ix *index.Index, sym string) []string {
	fileMap := buildFileMap(ix)
	var out []string
	for _, c := range ix.Callers[sym] {
		if f := fileMap[c]; f == "" || !isTestFile(f) {
			out = append(out, c)
		}
	}
	return out
}

// localNames returns every symbol name (full and simple) known to the index.
func localNames(ix *index.Index) map[string]bool {
	set := map[string]bool{}
	for _, s := range ix.Symbols {
		set[s.FullName()] = true
		set[s.Name] = true
	}
	return set
}

// localCallees returns the callees of sym that resolve to in-project symbols,
// filtering out stdlib / third-party calls. Package-qualified callees are
// normalised to the canonical in-project symbol name so graph traversals never
// dead-end on "pkg.Fn" while the symbol is indexed as "Fn".
func localCallees(ix *index.Index, sym string) []string {
	local := localNames(ix)
	var out []string
	for _, c := range ix.Calls[sym] {
		if c == sym {
			continue
		}
		if local[c] {
			out = append(out, c)
			continue
		}
		if s := simpleName(c); local[s] && s != sym {
			out = append(out, s)
		}
	}
	return out
}

// BlastRadius returns the transitive reverse closure of callers for the given
// roots: every symbol that (directly or transitively) calls one of the roots.
// The returned map records each symbol's distance from the nearest root.
func BlastRadius(ix *index.Index, roots []string) ([]string, map[string]int) {
	visited := map[string]int{}
	queue := make([]string, 0, len(roots))
	for _, r := range roots {
		if r == "" {
			continue
		}
		if _, ok := visited[r]; !ok {
			visited[r] = 0
			queue = append(queue, r)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		for _, caller := range ix.Callers[cur] {
			if _, ok := visited[caller]; !ok {
				visited[caller] = visited[cur] + 1
				queue = append(queue, caller)
			}
		}
	}
	out := make([]string, 0, len(visited))
	for s := range visited {
		out = append(out, s)
	}
	sort.Strings(out)
	return out, visited
}

// symbolsForFile returns the FullName of every symbol defined in rel.
func symbolsForFile(ix *index.Index, rel string) []string {
	syms := ix.SymbolsByFile[rel]
	out := make([]string, 0, len(syms))
	for _, s := range syms {
		if !isTestFile(s.File) {
			out = append(out, s.FullName())
		}
	}
	sort.Strings(out)
	return out
}

// AffectedFiles returns the distinct files touched by a set of symbols.
func AffectedFiles(ix *index.Index, symbols []string) []string {
	fileMap := buildFileMap(ix)
	seen := map[string]bool{}
	var out []string
	for _, s := range symbols {
		if f := fileMap[s]; f != "" && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	sort.Strings(out)
	return out
}

// ReadIndex loads the persisted index for root, rebuilding it on demand when
// it is absent, the on-disk version is incompatible, or any source file has
// been added/removed/edited since the index was built (content-hash manifest).
func ReadIndex(root string) (*index.Index, error) {
	if ix, err := index.Load(root); err == nil && ix != nil && !ix.Stale() {
		return ix, nil
	}
	ix, err := index.Build(root)
	if err != nil {
		return nil, err
	}
	_ = ix.Save()
	return ix, nil
}
