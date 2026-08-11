package intel

import (
	"fmt"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Flow is a critical execution flow rooted at an entry point. Depth is the
// longest call chain from the root; Reachable is the total number of symbols
// the root can reach (its out-blast).
type Flow struct {
	Root      string   `json:"root"`
	File      string   `json:"file"`
	Depth     int      `json:"depth"`
	Reachable int      `json:"reachable"`
	Path      []string `json:"path"`
}

// Flows traces execution flows from entry points (functions nobody in the
// project calls, plus conventional names like main/run). maxDepth caps the
// longest chain; limit caps how many flows are returned.
func Flows(ix *index.Index, limit, maxDepth int) []Flow {
	if limit <= 0 {
		limit = 10
	}
	if maxDepth <= 0 {
		maxDepth = 12
	}
	fileMap := buildFileMap(ix)

	// Roots: callable symbols with no in-project callers.
	var roots []string
	for _, s := range ix.Symbols {
		if isTestFile(s.File) || (s.Kind != "func" && s.Kind != "method") {
			continue
		}
		name := s.FullName()
		if len(ix.Callers[name]) > 0 {
			continue
		}
		if len(ix.Calls[name]) > 0 || isEntryPoint(name) || s.Entry {
			roots = append(roots, name)
		}
	}
	sort.Strings(roots)

	var flows []Flow
	for _, r := range roots {
		visited := map[string]bool{r: true}
		queue := []string{r}
		reachable := 0
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			for _, callee := range localCallees(ix, cur) {
				if !visited[callee] {
					visited[callee] = true
					reachable++
					queue = append(queue, callee)
				}
			}
		}
		if reachable == 0 {
			continue
		}
		path := longestPath(ix, r, visited, maxDepth)
		flows = append(flows, Flow{
			Root: r, File: fileMap[r], Depth: len(path), Reachable: reachable, Path: path,
		})
	}

	sort.Slice(flows, func(i, j int) bool {
		if flows[i].Reachable != flows[j].Reachable {
			return flows[i].Reachable > flows[j].Reachable
		}
		if flows[i].Depth != flows[j].Depth {
			return flows[i].Depth > flows[j].Depth
		}
		return flows[i].Root < flows[j].Root
	})
	// Dedupe flows that collapsed onto the same symbol name (e.g. a main()
	// defined in several files): keep the first (deepest/broadest).
	seen := map[string]bool{}
	unique := flows[:0]
	for _, f := range flows {
		key := strings.Join(f.Path, "\x00")
		if !seen[key] {
			seen[key] = true
			unique = append(unique, f)
		}
	}
	flows = unique
	if len(flows) > limit {
		flows = flows[:limit]
	}
	return flows
}

// longestPath returns the deepest call chain starting from root (capped at
// maxDepth), preferring routes through heavily-reached symbols.
func longestPath(ix *index.Index, root string, reachable map[string]bool, maxDepth int) []string {
	best := []string{root}
	var dfs func(node string, path []string, onPath map[string]bool)
	dfs = func(node string, path []string, onPath map[string]bool) {
		// Cycle guard: call graphs are cyclic, and re-entering a node already
		// on the current path would explode the search (or recurse to
		// maxDepth on every cycle). Skip on-path callees; when every callee
		// is on the path the branch ends here.
		callees := localCallees(ix, node)
		var next []string
		for _, c := range callees {
			if onPath[c] {
				continue
			}
			if reachable[c] {
				next = append(next, c)
			}
		}
		if len(next) == 0 {
			for _, c := range callees {
				if !onPath[c] {
					next = append(next, c)
				}
			}
		}
		if len(path) >= maxDepth || len(next) == 0 {
			if len(path) > len(best) {
				best = append([]string(nil), path...)
			}
			return
		}
		// Prefer callees that are still inside the reachable set.
		for _, c := range next[:min(len(next), 3)] {
			onPath[c] = true
			dfs(c, append(path, c), onPath)
			delete(onPath, c)
		}
	}
	dfs(root, []string{root}, map[string]bool{root: true})
	return best
}

// RenderFlows returns a compact human-readable flows report.
func RenderFlows(flows []Flow) string {
	var b strings.Builder
	b.WriteString("execution flows (from entry points, by reach):\n")
	for _, f := range flows {
		fmt.Fprintf(&b, "  %-24s %-24s depth %d · reach %d\n", f.Root, f.File, f.Depth, f.Reachable)
		if len(f.Path) > 1 {
			b.WriteString("    " + strings.Join(f.Path, " -> ") + "\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}
