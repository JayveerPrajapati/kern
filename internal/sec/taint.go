package sec

import (
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// TaintFinding is a sec finding plus its source→sink reachability analysis.
type TaintFinding struct {
	Finding
	Func       string   // containing function name, "<unknown>" if unresolvable
	Tainted    bool     // reachable from a source
	EntryPoint string   // entry symbol that starts the path ("" when none)
	Path       []string // entry → ... → sink function chain (cap 10, source-side first)
}

// sourcePatterns are expression fragments that indicate user-controlled input
// entering the file (request params, bodies, CLI args, raw readers). A sink
// file containing any of them is treated as source-reachable even when no
// entry-point path exists in the call graph.
var sourcePatterns = []string{
	"os.Args",
	".Query()",
	".FormValue(",
	".Param(",
	"r.Body",
	"req.Body",
	"io.ReadAll(r",
	"json.NewDecoder(",
}

const (
	// taintBFSDepth caps the reachability walk depth (edges from the sink).
	taintBFSDepth = 10
	// taintBFSNodes caps the total number of visited call-graph nodes per
	// finding so the analysis stays bounded on pathological graphs.
	taintBFSNodes = 500
)

// TaintLite marks findings whose containing function is transitively called
// by a framework entry point (Symbol.Entry) or whose file contains a source
// expression. Deterministic; BFS depth ≤ 10, ≤ 500 visited nodes per finding.
func TaintLite(ix *index.Index, findings []Finding) []TaintFinding {
	if len(findings) == 0 {
		return nil
	}
	out := make([]TaintFinding, 0, len(findings))
	for _, f := range findings {
		out = append(out, taintOne(ix, f))
	}
	return out
}

// taintOne analyses a single finding: resolve the containing function, run the
// bounded BFS over the reverse call graph, and fall back to the source-file
// check when no entry path exists.
func taintOne(ix *index.Index, f Finding) TaintFinding {
	tf := TaintFinding{Finding: f, Func: "<unknown>"}
	// G-4: Python findings carry no Go call-graph symbols. The sink symbol is
	// the matched callee and taint is decided by the source-file heuristic.
	if isPythonFinding(f) {
		tf.Func = pythonSinkSymbol(f)
		if ix != nil && sourceFileTainted(ix, f) {
			tf.Tainted = true
		}
		return tf
	}
	if ix == nil {
		return tf
	}
	fn, ok := containingFunc(ix, f)
	if !ok {
		return tf // unresolved line: no function to walk, not tainted
	}
	tf.Func = fn.FullName()

	// Source-file check: read root/<finding.File> (skip on error); a source
	// expression fragment anywhere in the file marks it as source-reachable.
	sourceFile := sourceFileTainted(ix, f)

	entrySet := entryNames(ix)
	// The sink function itself may be the entry (a handler containing the
	// sink directly); treat that as trivially reachable.
	if entrySet[fn.FullName()] || entrySet[fn.Name] {
		tf.Tainted = true
		tf.EntryPoint = fn.FullName()
		tf.Path = []string{fn.FullName()}
		return tf
	}
	if entry, path, ok := bfsToEntry(ix, fn, entrySet); ok {
		tf.Tainted = true
		tf.EntryPoint = entry
		tf.Path = path
		return tf
	}
	if sourceFile {
		tf.Tainted = true
	}
	return tf
}

// containingFunc returns the innermost indexed symbol whose span (Line..End)
// covers the finding line. Only functions/methods qualify; symbols with an
// unknown end (End == 0) are skipped. ok is false when nothing matches.
func containingFunc(ix *index.Index, f Finding) (index.Symbol, bool) {
	var best index.Symbol
	bestSpan := -1
	found := false
	for _, s := range ix.Symbols {
		if s.Kind != "func" && s.Kind != "method" {
			continue
		}
		if s.File != f.File || s.End <= 0 || f.Line < s.Line || f.Line > s.End {
			continue
		}
		span := s.End - s.Line
		if !found || span < bestSpan {
			best = s
			bestSpan = span
			found = true
		}
	}
	return best, found
}

// entryNames collects the full (and bare) names of every framework entry
// symbol in the index.
func entryNames(ix *index.Index) map[string]bool {
	set := make(map[string]bool)
	for _, s := range ix.Symbols {
		if s.Entry {
			set[s.FullName()] = true
			set[s.Name] = true
		}
	}
	return set
}

// sourceFileTainted reports whether root/<f.File> contains a source
// expression fragment anywhere in its content (skip on read error).
func sourceFileTainted(ix *index.Index, f Finding) bool {
	data, err := os.ReadFile(filepath.Join(ix.Root, filepath.FromSlash(f.File)))
	if err != nil {
		return false
	}
	content := string(data)
	for _, p := range sourcePatterns {
		if strings.Contains(content, p) {
			return true
		}
	}
	return false
}

// isPythonFinding reports whether the finding originates from a Python file
// or a Python-specific rule (G-4); such findings have no Go call-graph path.
func isPythonFinding(f Finding) bool {
	return strings.HasPrefix(f.Rule, "py-") || strings.HasSuffix(strings.ToLower(f.File), ".py")
}

// pythonSinkSymbol maps a Python rule id to the matched callee, used as the
// sink's containing-function name in taint output.
func pythonSinkSymbol(f Finding) string {
	switch f.Rule {
	case "py-eval":
		return "eval"
	case "py-exec":
		return "exec"
	case "py-os-system":
		return "os.system"
	case "py-subprocess-shell", "py-subprocess":
		return "subprocess"
	case "py-pickle-load":
		return "pickle.loads"
	case "py-yaml-load":
		return "yaml.load"
	case "py-sql-format":
		return "execute"
	}
	return "<python>"
}

// simpleName returns the part of a (possibly package-qualified) name after
// the last '.'; plain names are returned unchanged.
func simpleName(name string) string {
	if i := strings.LastIndexByte(name, '.'); i >= 0 {
		return name[i+1:]
	}
	return name
}

// bfsToEntry walks the reverse call graph from fn up to depth 10 (≤ 500
// visited nodes), looking for a framework entry point. On a hit it returns
// the entry name and the source-side-first path entry → ... → sink.
func bfsToEntry(ix *index.Index, fn index.Symbol, entrySet map[string]bool) (string, []string, bool) {
	start := fn.FullName()
	// Queue the resolved name plus its bare last-segment variant: cross-package
	// keys are qualified ("util.F") while local symbols are bare, and the graph
	// may record either form for the same callee.
	seeds := []string{start}
	if simple := simpleName(start); simple != start {
		seeds = append(seeds, simple)
	}

	visited := make(map[string]bool)
	parent := make(map[string]string) // caller -> callee (closer to sink); "" marks the sink
	queue := make([]string, 0, 16)
	depth := make(map[string]int)
	for _, n := range seeds {
		if !visited[n] {
			visited[n] = true
			parent[n] = ""
			depth[n] = 0
			queue = append(queue, n)
		}
	}

	neighbors := func(name string) []string {
		seen := make(map[string]bool)
		var out []string
		for _, n := range ix.Callers[name] {
			if !seen[n] {
				seen[n] = true
				out = append(out, n)
			}
		}
		// Qualified callee keys also expose callers under the bare name.
		if simple := simpleName(name); simple != name {
			for _, n := range ix.Callers[simple] {
				if !seen[n] {
					seen[n] = true
					out = append(out, n)
				}
			}
		}
		sort.Strings(out) // deterministic neighbor ordering
		return out
	}

	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if depth[cur] >= taintBFSDepth {
			continue
		}
		for _, n := range neighbors(cur) {
			if n == cur {
				continue
			}
			if !visited[n] {
				if len(visited) >= taintBFSNodes {
					return "", nil, false
				}
				visited[n] = true
				parent[n] = cur
				depth[n] = depth[cur] + 1
				queue = append(queue, n)
			}
			if entrySet[n] || entrySet[simpleName(n)] {
				return n, buildTaintPath(n, parent, start), true
			}
		}
	}
	return "", nil, false
}

// buildTaintPath reconstructs the chain entry → ... → sink from the BFS parent
// pointers (parent[X] is the callee X calls, one step closer to the sink).
// The result is capped at 10 entries, source-side first.
func buildTaintPath(entry string, parent map[string]string, start string) []string {
	path := []string{entry}
	cur := parent[entry]
	for cur != "" && len(path) < taintBFSDepth {
		path = append(path, cur)
		cur = parent[cur]
	}
	// The BFS may reach the sink through its bare-name alias; always terminate
	// the chain with the resolved FullName.
	if last := path[len(path)-1]; last != start {
		if parent[last] == "" {
			path[len(path)-1] = start
		} else if len(path) < taintBFSDepth {
			path = append(path, start)
		}
	}
	return path
}
