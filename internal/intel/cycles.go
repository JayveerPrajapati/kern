package intel

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// ImportCycle is one package-level import cycle: the packages (directories)
// that form it, plus per-edge evidence (the file that imports the next
// package and the line of the import statement when it can be located).
type ImportCycle struct {
	Packages []string    `json:"packages"` // sorted, deterministic
	Edges    []CycleEdge `json:"edges"`    // deterministic evidence
}

// CycleEdge is one import within a cycle: From (package dir) imports To
// (package dir) in File at Line.
type CycleEdge struct {
	From string `json:"from"`
	To   string `json:"to"`
	File string `json:"file"`
	Line int    `json:"line"`
}

// ImportCycles returns every package-level import cycle in the index via
// Tarjan's strongly-connected-components algorithm over the project-local
// import graph (a self-import is a single-package cycle). Only project
// packages are nodes; third-party imports are ignored. The result is
// deterministic: cycles sorted by their smallest member, members sorted, and
// edge evidence sorted by file.
func ImportCycles(ix *index.Index) []ImportCycle {
	if ix == nil || len(ix.Pkgs) == 0 {
		return nil
	}
	graph, evidence := packageImportGraph(ix)
	sccs := tarjanSCCs(graph)
	var cycles []ImportCycle
	for _, scc := range sccs {
		if len(scc) == 1 {
			// Self-loop: a package importing itself is a degenerate cycle.
			if !listContains(graph[scc[0]], scc[0]) {
				continue
			}
		}
		cycle := ImportCycle{Packages: append([]string{}, scc...)}
		sort.Strings(cycle.Packages)
		// Evidence: every edge whose endpoints are both in the cycle.
		for _, e := range evidence {
			if listContains(cycle.Packages, e.From) && listContains(cycle.Packages, e.To) {
				cycle.Edges = append(cycle.Edges, e)
			}
		}
		sort.Slice(cycle.Edges, func(i, j int) bool {
			if cycle.Edges[i].From != cycle.Edges[j].From {
				return cycle.Edges[i].From < cycle.Edges[j].From
			}
			if cycle.Edges[i].To != cycle.Edges[j].To {
				return cycle.Edges[i].To < cycle.Edges[j].To
			}
			return cycle.Edges[i].File < cycle.Edges[j].File
		})
		cycles = append(cycles, cycle)
	}
	sort.Slice(cycles, func(i, j int) bool {
		return cycles[i].Packages[0] < cycles[j].Packages[0]
	})
	return cycles
}

// stringList is a tiny slice wrapper for containment checks on sorted lists.
type stringList []string

func (l stringList) contains(s string) bool {
	i := sort.SearchStrings(l, s)
	return i < len(l) && l[i] == s
}

func listContains(l []string, s string) bool {
	return stringList(l).contains(s)
}

// packageImportGraph builds the project-local package import graph (dir ->
// sorted dirs it imports) and the per-edge evidence (file + line). Edges come
// from per-file imports (ix.ImportsByFile) when present — that is the exact
// attribution guard relies on — falling back to package-aggregated imports
// (Pkgs[dir].Imports) for indexes built before per-file imports existed.
func packageImportGraph(ix *index.Index) (map[string][]string, []CycleEdge) {
	dirs := make([]string, 0, len(ix.Pkgs))
	for dir := range ix.Pkgs {
		dirs = append(dirs, dir)
	}
	sort.Strings(dirs)
	graph := map[string][]string{}
	var evidence []CycleEdge
	lineCache := map[string]map[string]int{} // file -> import path -> line

	importLine := func(file, importPath string) int {
		if m, ok := lineCache[file]; ok {
			return m[importPath]
		}
		m := map[string]int{}
		if data, err := os.ReadFile(filepath.Join(ix.Root, file)); err == nil {
			sc := bufio.NewScanner(strings.NewReader(string(data)))
			for ln := 1; sc.Scan(); ln++ {
				line := sc.Text()
				for _, cand := range []string{`"` + importPath + `"`, importPath} {
					if strings.Contains(line, cand) && !strings.Contains(line, "//") {
						if _, ok := m[cand]; !ok {
							m[importPath] = ln
						}
					}
				}
			}
		}
		lineCache[file] = m
		return m[importPath]
	}

	addEdge := func(fromDir, toDir, file, importPath string) {
		if fromDir == "" || toDir == "" {
			return
		}
		if !listContains(graph[fromDir], toDir) {
			graph[fromDir] = append(graph[fromDir], toDir)
		}
		evidence = append(evidence, CycleEdge{
			From: fromDir, To: toDir,
			File: file, Line: importLine(file, importPath),
		})
	}

	for _, dir := range dirs {
		pkg := ix.Pkgs[dir]
		if pkg == nil {
			continue
		}
		// Per-file imports: exact attribution. An import path resolves to
		// exactly ONE local package: the longest dir that matches it.
		// Matching against every dir would bind ".../blueprint/service" to
		// the parent "internal/blueprint" too, fabricating cross-package
		// edges and false cycles.
		// Go test files are not part of the production package dependency
		// DAG: their imports (often the tested package's siblings) would
		// fabricate cycles Go's compiler never sees.
		perFileConsulted := false
		for _, f := range pkg.Files {
			if strings.HasSuffix(f, "_test.go") {
				continue
			}
			imports, ok := ix.ImportsByFile[f]
			if !ok {
				continue
			}
			perFileConsulted = true
			for _, imp := range imports {
				if to := longestMatchDir(importMatches, imp.Path, dirs); to != "" {
					addEdge(dir, to, f, imp.Path)
				}
			}
		}
		// Fall back to package-aggregated imports ONLY for indexes built
		// before per-file import data existed; a modern index whose
		// non-test files carry no imports records zero edges, never the
		// test-inclusive aggregate.
		if perFileConsulted {
			continue
		}
		// Fallback: package-aggregated imports.
		for _, imp := range pkg.Imports {
			if to := longestMatchDir(importMatches, imp.Path, dirs); to != "" {
				addEdge(dir, to, firstFile(pkg), imp.Path)
			}
		}
	}
	for dir := range graph {
		sort.Strings(graph[dir])
	}
	return graph, evidence
}

// longestMatchDir returns the longest dir in dirs that matches importPath,
// or "" when none does. importMatches is a func value so the caller's
// matching policy (Go slash + Java dotted) applies unchanged.
func longestMatchDir(matches func(importPath, dir string) bool, importPath string, dirs []string) string {
	bestDir, bestLen := "", 0
	for _, to := range dirs {
		if matches(importPath, to) && len(to) > bestLen {
			bestDir, bestLen = to, len(to)
		}
	}
	return bestDir
}

// firstFile returns the first source file of a package, or "" for a
// package with no files.
func firstFile(pkg *index.Pkg) string {
	if pkg == nil || len(pkg.Files) == 0 {
		return ""
	}
	return pkg.Files[0]
}

// tarjanSCCs computes strongly connected components iteratively (recursion
// depth on a repo-sized package graph would overflow the stack).
func tarjanSCCs(graph map[string][]string) [][]string {
	const (
		unvisited = 0
		active    = 1
		done      = 2
	)
	index := map[string]int{}
	lowlink := map[string]int{}
	state := map[string]int{}
	stack := []string{}
	onStack := map[string]bool{}
	var sccs [][]string
	counter := 0

	type frame struct {
		node string
		next int
	}
	for _, start := range sortedKeys(graph) {
		if state[start] != unvisited {
			continue
		}
		frames := []frame{{node: start}}
		for len(frames) > 0 {
			f := &frames[len(frames)-1]
			n := f.node
			if state[n] == unvisited {
				state[n] = active
				index[n] = counter
				lowlink[n] = counter
				counter++
				stack = append(stack, n)
				onStack[n] = true
			}
			neighbors := graph[n]
			recursed := false
			for f.next < len(neighbors) {
				w := neighbors[f.next]
				f.next++
				switch state[w] {
				case unvisited:
					frames = append(frames, frame{node: w})
					recursed = true
				case active:
					if index[w] < lowlink[n] {
						lowlink[n] = index[w]
					}
				}
				if recursed {
					break
				}
			}
			if recursed {
				continue
			}
			// All neighbors visited: pop frames and unwind lowlinks.
			if len(frames) > 1 {
				parent := frames[len(frames)-2]
				if lowlink[n] < lowlink[parent.node] {
					lowlink[parent.node] = lowlink[n]
				}
			}
			frames = frames[:len(frames)-1]
			if lowlink[n] == index[n] {
				var comp []string
				for {
					w := stack[len(stack)-1]
					stack = stack[:len(stack)-1]
					onStack[w] = false
					state[w] = done
					comp = append(comp, w)
					if w == n {
						break
					}
				}
				sort.Strings(comp)
				sccs = append(sccs, comp)
			}
		}
	}
	sort.Slice(sccs, func(i, j int) bool { return sccs[i][0] < sccs[j][0] })
	return sccs
}

func sortedKeys(graph map[string][]string) []string {
	keys := make([]string, 0, len(graph))
	for k := range graph {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

// ImportCycleWarnings returns human-readable warnings for changed files whose
// package participates in an import cycle (the guard gate: cycle touched by a
// diff = warn, never block). Deterministic order.
func ImportCycleWarnings(ix *index.Index, files []string) []string {
	if len(files) == 0 {
		return nil
	}
	cycles := ImportCycles(ix)
	if len(cycles) == 0 {
		return nil
	}
	changedDirs := map[string]bool{}
	for _, f := range files {
		if dir := filepath.Dir(f); dir != "." {
			changedDirs[dir] = true
		}
	}
	var warnings []string
	for _, c := range cycles {
		involved := false
		for _, p := range c.Packages {
			if changedDirs[p] {
				involved = true
				break
			}
		}
		if !involved {
			continue
		}
		warnings = append(warnings, fmt.Sprintf("import cycle %s", cycleLabel(c)))
	}
	return warnings
}

func cycleLabel(c ImportCycle) string {
	var b strings.Builder
	for i, p := range c.Packages {
		if i > 0 {
			b.WriteString(" -> ")
		}
		b.WriteString(p)
	}
	return b.String()
}

// RenderCycles renders the cycle list for the terminal: count, then one block
// per cycle with its members and file:line evidence.
func RenderCycles(cycles []ImportCycle) string {
	if len(cycles) == 0 {
		return "no import cycles (project package graph is acyclic)"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%d import cycle(s):\n", len(cycles))
	for i, c := range cycles {
		fmt.Fprintf(&b, "\ncycle %d (%d packages): %s\n", i+1, len(c.Packages), cycleLabel(c))
		for _, e := range c.Edges {
			loc := e.File
			if e.Line > 0 {
				loc = fmt.Sprintf("%s:%d", e.File, e.Line)
			}
			fmt.Fprintf(&b, "  %s -> %s  (%s)\n", e.From, e.To, loc)
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
