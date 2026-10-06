// Package intel turns kern's AST index into a code-intelligence engine: change
// impact (blast radius + risk), test-coverage gaps, hub/bridge hotspots,
// execution flows and community clustering — pure Go over the persisted index.
package intel

import (
	"log"
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

// Error implements the error interface for GitError.
func (e *GitError) Error() string {
	return "git failed (" + e.Op + "): " + e.Err.Error()
}

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

// ImportMatches reports whether an import path refers to a local directory.
// Go import paths are slash-separated; Java (and other JVM languages) use
// dotted package paths, so a slash-converted variant is tested as well. It
// lives here (shared with the guard package, which checks imports against
// boundary rules) because intel's own cycle analyzer consumes it too.
func ImportMatches(importPath, dir string) bool {
	if importPath == "" || dir == "" {
		return false
	}
	// Go-style (slash) imports: the module-relative package dir is a suffix of
	// the full import path ("github.com/x/y/internal/z" <-> "internal/z").
	if strings.HasSuffix(importPath, "/"+dir) || importPath == dir {
		return true
	}
	// Java-style (dotted) imports: the package path is a suffix of the source
	// directory ("com.example.pkg.foo" <-> ".../java/com/example/pkg/foo"). The full
	// package path is required; basename-only matches cross shared suffixes.
	if strings.Contains(importPath, ".") {
		slash := strings.ReplaceAll(importPath, ".", "/")
		return slash == dir || strings.HasSuffix(dir, "/"+slash)
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
			full := s.FullName()
			// Duplicate names (a production func plus a same-named test
			// helper) must resolve to the PRODUCTION file: a test-file def
			// shadowing a live caller misclassifies it as test-only and
			// reports the symbol safe to delete.
			if cur, ok := m[full]; !ok || (IsTestFile(cur) && !IsTestFile(s.File)) {
				m[full] = s.File
			}
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

// prodCallersWithFileMap is prodCallers with a precomputed file map. Callers
// that iterate over the whole symbol table MUST hoist buildFileMap(ix) out of
// their loop — building it per symbol is O(len(Symbols)) inside an
// O(len(Symbols)) loop (quadratic on large repos).
func prodCallersWithFileMap(ix *index.Index, sym string, fileMap map[string]string) []string {
	var out []string
	for _, c := range ix.Callers[sym] {
		if f := fileMap[c]; f == "" || !IsTestFile(f) {
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
// Leading-dot targets (".Str.Int.Msg" fluent-chain fragments from the regex
// extractors) are skipped outright: they are never real symbols, and their
// simple-name tail ("Msg") could otherwise resolve to an unrelated local
// symbol and inflate callee lists (F5, deep-dive 2026-10-03).
func localCalleesWith(ix *index.Index, sym string, local map[string]bool) []string {
	var out []string
	for _, ce := range ix.Calls[sym] {
		c := ce.Target
		if c == sym || strings.HasPrefix(c, ".") {
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
	reach, _, dist, _ := blastRadiusWalk(ix, roots, false)
	return reach, dist
}

// BlastRadiusPrecise is BlastRadius with a precision mode. When strict is
// true, call edges whose caller language is not "resolved"-precision in the
// index (ix.PrecisionByLang) are skipped: the caller is reported as unknown
// rather than guessed into the blast radius. The third return value is the
// number of heuristic edges skipped.
func BlastRadiusPrecise(ix *index.Index, roots []string, strict bool) ([]string, map[string]int, int) {
	reach, _, dist, skipped := blastRadiusWalk(ix, roots, strict)
	return reach, dist, skipped
}

// blastRadiusWalk is the package-aware blast-radius BFS. The visited set is
// keyed by symbol NAME (first-seen depth, sorted for determinism), exactly
// like the pre-package-aware walk, but the neighbors come from the same
// package-aware attribution explore's direct-caller list uses:
// ix.CallersFor(sym) resolves each recorded caller of a symbol to the
// symbols that really call it (package-qualified keys plus same-package bare
// buckets — never a bare-name merge of every same-named definition), and
// findCallerDef resolves each caller endpoint to the concrete symbol with
// the parent as context (same-package, then import-based). Because the
// visited set is keyed by name, a same-named symbol that is itself a caller
// (the MCP wrapper "AuthorizeContext" calling the governance core) collapses
// into the already-visited root instead of re-expanding every same-named
// definition's edges — the same-name merge can no longer amplify the radius
// (TestAuthorizeContextDefaultScoped/Denial call the wrapper, not the core).
// Roots that do not resolve to an indexed symbol keep the legacy raw
// ix.Callers expansion. The second return value carries the resolved symbol
// behind each radius name (same order) so file attribution can use the exact
// symbol instead of a first-match name map ("Generate" is shared by three
// packages; only evidence's calls the governance core).
func blastRadiusWalk(ix *index.Index, roots []string, strict bool) ([]string, []index.Symbol, map[string]int, int) {
	visited := map[string]int{}
	type entry struct {
		name string
		sym  index.Symbol
	}
	queue := make([]entry, 0, len(roots))
	skipped := 0
	symByName := map[string]index.Symbol{}
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
		if _, ok := visited[r]; ok {
			continue
		}
		visited[r] = 0
		sym, ok := findDef(ix, r)
		if ok {
			symByName[r] = sym
		}
		queue = append(queue, entry{name: r, sym: sym})
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur.sym.File == "" {
			// A root that does not resolve to an indexed symbol keeps the
			// legacy raw-name expansion (callers recorded under its name).
			for _, caller := range ix.Callers[cur.name] {
				if strict {
					// Strict precision: an edge whose caller language is not
					// fully resolved is unknown, not guessable, so the caller
					// is skipped.
					if p := ix.PrecisionByLang[langByFull[caller]]; p != "resolved" {
						skipped++
						continue
					}
				}
				if _, ok := visited[caller]; !ok {
					visited[caller] = visited[cur.name] + 1
					queue = append(queue, entry{name: caller})
				}
			}
			continue
		}
		for _, c := range ix.CallersFor(cur.sym) {
			callerSym, ok := findCallerDef(ix, c, cur.sym)
			if !ok {
				// An unresolvable caller endpoint is still a real caller:
				// report it under its raw name as a leaf (never expanded).
				if strict {
					if p := ix.PrecisionByLang[langByFull[c]]; p != "resolved" {
						skipped++
						continue
					}
				}
				if _, ok := visited[c]; !ok {
					visited[c] = visited[cur.name] + 1
					queue = append(queue, entry{name: c})
				}
				continue
			}
			if strict {
				if p := ix.PrecisionByLang[callerSym.Lang]; p != "resolved" {
					skipped++
					continue
				}
			}
			name := callerSym.FullName()
			if _, ok := visited[name]; !ok {
				visited[name] = visited[cur.name] + 1
				symByName[name] = callerSym
				queue = append(queue, entry{name: name, sym: callerSym})
			}
		}
	}
	out := make([]string, 0, len(visited))
	for s := range visited {
		out = append(out, s)
	}
	sort.Strings(out)
	// outSyms must stay POSITIONALLY PARALLEL to out — the "same order"
	// contract the doc comment above promises and ExploreBudgeted's depth
	// cap indexes radiusSyms[i] against it. A radius member that resolves to
	// no indexed symbol (the Python/JS heuristic-caller case: blast-radius
	// leaves are raw endpoints findCallerDef cannot resolve) keeps its
	// zero-value slot here; the pre-fix filtered append shifted every later
	// symbol one left, which both misattributed BlastFiles and panicked
	// with "index out of range" on non-Go symbols (live campaign 2026-10-04:
	// every Python-class and JS-function kern explore crashed here).
	// affectedFilesOf already skips zero-value Symbols (File == "").
	outSyms := make([]index.Symbol, len(out))
	for i, s := range out {
		if sym, ok := symByName[s]; ok {
			outSyms[i] = sym
		}
	}
	return out, outSyms, visited, skipped
}

// affectedFilesOf returns the distinct non-documentation source files of the
// given symbols, sorted — the symbol-identity analogue of AffectedFiles (a
// bare name like "Generate" is shared by several packages, so a name-keyed
// file map would attribute it to the wrong file).
func affectedFilesOf(syms []index.Symbol) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range syms {
		if s.File == "" || index.IsDocFile(s.File) || seen[s.File] {
			continue
		}
		seen[s.File] = true
		out = append(out, s.File)
	}
	sort.Strings(out)
	return out
}

// AffectedFiles returns the distinct files touched by a set of symbols.
// Documentation files (markdown/HTML — see index.IsDocFile) are excluded:
// the index keeps them in the graph, but a code blast-radius set is about
// what a change can break in code, and a fuzzy over-match dragged doc pages
// (export_graph.html, docs/adr/*.md) into every impacted-file list.
func AffectedFiles(ix *index.Index, symbols []string) []string {
	fileMap := buildFileMap(ix)
	seen := map[string]bool{}
	var out []string
	for _, s := range symbols {
		if f := fileMap[s]; f != "" && !index.IsDocFile(f) && !seen[f] {
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
	ix, _, err := ReadIndexWithProof(root)
	return ix, err
}

// ReadIndexWithProof is ReadIndex plus the freshness proof of the index it
// returns: the staleness decision ReadIndex already makes internally, or a
// re-observation after an incremental update, so callers that need both (e.g.
// `kern guard check`, whose JSON output carries the provenance) observe the
// tree ONCE instead of twice. The returned proof reflects the returned index:
// fresh on the fresh path, and post-update on the update path.
func ReadIndexWithProof(root string) (*index.Index, index.FreshnessProof, error) {
	prev, lerr := index.Load(root)
	if lerr == nil && prev != nil {
		stale, proof := prev.StaleWithProof(root)
		if !stale {
			return prev, proof, nil
		}
		// A loadable-but-stale index is refreshed INCREMENTALLY (index.Update
		// re-parses only changed/new files and copies symbols/edges of unchanged
		// files verbatim — the same update-over-build pattern LoadOrBuild and
		// project.Session.rebuildIndex use). Previously this fell straight to a
		// full index.Build, so every commit (even a 2-file one) invalidated the
		// whole index and forced a full re-parse on the next `kern guard check`.
		// Update produces an index equivalent to a full rebuild, so freshness
		// proofs and staleness detection behave identically; any Update failure
		// falls back to the full Build.
		if ix, err := index.Update(root, prev); err == nil && ix != nil {
			if serr := ix.Save(); serr != nil {
				log.Printf("intel: incremental index save failed (next load will re-index): %v", serr)
			}
			// Post-update proof: observe the tree the served index claims to
			// reflect (expected: fresh — Update records the current identity).
			return ix, ix.FreshnessProof(root), nil
		}
	}
	ix, err := index.Build(root)
	if err != nil {
		return nil, index.FreshnessProof{}, err
	}
	if serr := ix.Save(); serr != nil {
		log.Printf("intel: index save failed (next load will re-index): %v", serr)
	}
	return ix, ix.FreshnessProof(root), nil
}
