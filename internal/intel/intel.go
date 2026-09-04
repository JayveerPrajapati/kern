// Package intel turns kern's AST index into a code-intelligence engine: change
// impact (blast radius + risk), test-coverage gaps, hub/bridge hotspots,
// execution flows and community clustering — pure Go over the persisted index.
package intel

import (
	"os/exec"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// ChangedFiles returns the files changed in the working tree (staged +
// unstaged) relative to HEAD. Falls back to `git status --porcelain -z` when
// the repository has no commits yet.
func ChangedFiles(root string) ([]string, error) {
	cmd := exec.Command("git", "-C", root, "diff", "HEAD", "--name-only")
	out, err := cmd.Output()
	if err != nil {
		out, err = exec.Command("git", "-C", root, "status", "--porcelain", "-z").Output()
		if err != nil {
			return nil, &GitError{Op: "git status --porcelain -z", Err: err}
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

// parsePorcelain parses NUL-separated `git status --porcelain -z` output. Each
// record is `XY <path>`; a rename/copy status is followed by a record holding
// the original path, which is skipped. NUL separation means paths are never
// escaped, so names with spaces and non-ASCII round-trip verbatim.
func parsePorcelain(out string) []string {
	var files []string
	records := strings.Split(out, "\x00")
	for i := 0; i < len(records); i++ {
		r := records[i]
		if len(r) < 3 || r[2] != ' ' {
			continue
		}
		files = append(files, r[3:])
		if r[0] == 'R' || r[0] == 'C' || r[1] == 'R' || r[1] == 'C' {
			i++ // skip the original-path continuation record
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
// Only the final segment matters ("Server.Run" is an entry point because it
// is named Run).
func isEntryPoint(name string) bool {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		name = name[i+1:]
	}
	switch name {
	case "main", "init", "run", "Run", "Main", "setup", "Setup", "start", "Start":
		return true
	}
	return false
}

// simpleName returns the part of a name after the last '.' ("" for a plain
// name). It is used for display and deduplication only.
func simpleName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
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
	return prodCallersWithFileMap(ix, sym, buildFileMap(ix))
}

// prodCallersWithFileMap is prodCallers with a precomputed file map. Callers
// that iterate over the whole symbol table MUST hoist buildFileMap(ix) out of
// their loop — building it per symbol is O(len(Symbols)) inside an
// O(len(Symbols)) loop (quadratic on large repos).
func prodCallersWithFileMap(ix *index.Index, sym string, fileMap map[string]string) []string {
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

// canonicalNames maps every symbol name to its canonical in-project FullName:
// simple names first, then any form the analyzer recorded for a call target.
// A simple name that collides (e.g. two types both defining "Save") maps to
// the qualified forms only — the bare form is left unmapped so graph
// traversals never forge an edge to the wrong receiver type. This is what
// lets kern path/near/walk bridge method calls recorded under a
// receiver-instance form ("store.Open.Save") to the method definition
// ("Store.Save").
func canonicalNames(ix *index.Index) map[string]string {
	bySimple := map[string][]string{}
	for _, s := range ix.Symbols {
		if s.Name == "" {
			continue
		}
		bySimple[s.Name] = append(bySimple[s.Name], s.FullName())
	}
	out := map[string]string{}
	for _, s := range ix.Symbols {
		f := s.FullName()
		out[f] = f
		if s.Name == "" {
			continue
		}
		if len(bySimple[s.Name]) == 1 {
			out[s.Name] = f
		}
	}
	return out
}

// canon resolves name to its canonical FullName via m, falling back to name
// itself when unmapped (foreign, ambiguous-simple, or already canonical).
func canon(m map[string]string, name string) string {
	if c, ok := m[name]; ok {
		return c
	}
	return name
}

// localCalleesWith returns the callees of sym that resolve to in-project
// symbols, using a precomputed local-name set. Callers that iterate over the
// whole symbol table MUST hoist localNames(ix) out of their loop and pass it
// here — recomputing it per symbol is O(len(Symbols)) inside an O(len(Symbols))
// loop, i.e. quadratic time on large repos.
func localCalleesWith(ix *index.Index, sym string, local map[string]bool) []string {
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
// Default precision: all edges are trusted.
func BlastRadius(ix *index.Index, roots []string) ([]string, map[string]int) {
	reach, dist, _ := BlastRadiusPrecise(ix, roots, false)
	return reach, dist
}

// BlastRadiusPrecise is BlastRadius with a precision mode. When strict is
// true, call edges whose caller language is not "resolved"-precision in the
// index (ix.PrecisionByLang) are skipped: the caller is reported as unknown
// rather than guessed into the blast radius. The third return value is the
// number of heuristic edges skipped.
func BlastRadiusPrecise(ix *index.Index, roots []string, strict bool) ([]string, map[string]int, int) {
	visited := map[string]int{}
	queue := make([]string, 0, len(roots))
	skipped := 0
	var langByFull map[string]string
	if strict {
		langByFull = map[string]string{}
		for _, s := range ix.Symbols {
			if _, ok := langByFull[s.FullName()]; !ok {
				langByFull[s.FullName()] = s.Lang
			}
		}
	}
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
			if strict {
				// Strict precision: an edge whose caller language is not fully
				// resolved is unknown, not guessable, so the caller is skipped.
				if p := ix.PrecisionByLang[langByFull[caller]]; p != "resolved" {
					skipped++
					continue
				}
			}
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
	return out, visited, skipped
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
