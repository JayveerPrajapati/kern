// Package verify cross-checks an agent's output text against the real source
// tree and index: every referenced file:line, symbol name and route is
// confirmed to exist (or flagged as unverifiable / missing). It is a cheap,
// deterministic hallucination check — no LLM involved.
package verify

import (
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Type of a reference check.
type Type string

const (
	Sym     Type = "symbol"
	FileRef Type = "file:line"
	Route   Type = "route"
	// Call is a natural-language call-graph claim ("X is called from Y" /
	// "X calls Y") verified against the indexed call edges (F-017).
	Call Type = "call"
)

// Check is the verdict for one extracted reference.
type Check struct {
	Type  Type
	Ref   string
	Found bool
	// Detail explains why it passed or failed.
	Detail string
}

// Report is the outcome of verifying a text.
type Report struct {
	Checks []Check
	// Missing lists the references that could not be confirmed.
	Missing []string
	OK      bool
}

var (
	fileLineRe = regexp.MustCompile(`\b([\w./-]+\.(?:go|py|js|ts|jsx|tsx|rs|rb|php|java|c|h|cpp|hpp|cs|kt|swift|vue|svelte|md|json|yaml|yml|toml|html|css)):(\d+)\b`)
	symbolRe   = regexp.MustCompile(`\b[A-Za-z_][A-Za-z0-9_]*(?:\.[A-Za-z_][A-Za-z0-9_]*)*\b`)
	routeRe    = regexp.MustCompile(`/(?:[A-Za-z0-9_\-{}.]+/?){1,5}`)
	// callFromRe matches "X is called from Y" — a claim that Y calls X.
	callFromRe = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_.]*)\s+is called from\s+([A-Za-z_][A-Za-z0-9_.]*)\b`)
	// callsRe matches "X calls Y" — a claim that X calls Y.
	callsRe = regexp.MustCompile(`\b([A-Za-z_][A-Za-z0-9_.]*)\s+calls\s+([A-Za-z_][A-Za-z0-9_.]*)\b`)
)

// Verify extracts code references from text and checks each against the
// indexed project. root is used for raw-file reads when the index lacks the
// file. ix may be nil (symbol/route checks then fall back to the raw source).
func Verify(ix *index.Index, root, text string) Report {
	var rep Report
	seen := map[string]bool{}
	add := func(t Type, ref string, found bool, detail string) {
		key := string(t) + "|" + ref
		if seen[key] {
			return
		}
		seen[key] = true
		rep.Checks = append(rep.Checks, Check{Type: t, Ref: ref, Found: found, Detail: detail})
		if !found {
			rep.Missing = append(rep.Missing, ref)
		}
	}

	for _, m := range fileLineRe.FindAllStringSubmatch(text, -1) {
		file, line := m[1], atoi(m[2])
		ok, detail := checkFileLine(ix, root, file, line)
		add(FileRef, file+":"+m[2], ok, detail)
	}

	fileMap := map[string]string{}
	if ix != nil {
		// Register all indexed symbols as candidate matches, and build
		// file->realpath for raw reads.
		for _, s := range ix.Symbols {
			fileMap[s.File] = s.File
			full := s.FullName()
			for _, m := range symbolRe.FindAllString(text, -1) {
				if m == full || (m == s.Name && isExported(s.Name)) {
					add(Sym, m, true, "indexed at "+s.File+":"+strconv.Itoa(s.Line))
				}
			}
		}
		for _, s := range ix.Symbols {
			if !s.Entry || s.Route == "" {
				continue
			}
			for _, m := range routeRe.FindAllString(text, -1) {
				m = strings.TrimRight(m, ".,;:)!?]}")
				if s.Route == m || strings.HasSuffix(s.Route, m) {
					add(Route, m, true, "registered route in "+s.File)
				}
			}
		}
	}

	// Any remaining route-like strings are reported as unregistered. Paths
	// that are clearly not routes are skipped: existing filesystem paths
	// (or paths under an existing ancestor) and date-like /YYYY/MM/DD.
	for _, m := range routeRe.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:)!?]}")
		if m == "" || !looksLikeRoute(m, root) {
			continue
		}
		if !seen["route|"+m] {
			add(Route, m, false, "no indexed handler registers this route")
		}
	}

	// Natural-language call-graph claims (F-017): "X is called from Y" means
	// Y calls X, "X calls Y" means Y is among X's callees. Both are verified
	// against the index's call edges. Conservative by design — only these two
	// sentence shapes are parsed; anything else is left to the symbol checks.
	for _, m := range callFromRe.FindAllStringSubmatch(text, -1) {
		callee, caller := m[1], m[2]
		ok, detail := checkCallClaim(ix, caller, callee)
		add(Call, callee+" <- "+caller, ok, detail)
	}
	for _, m := range callsRe.FindAllStringSubmatch(text, -1) {
		caller, callee := m[1], m[2]
		ok, detail := checkCallClaim(ix, caller, callee)
		add(Call, caller+" -> "+callee, ok, detail)
	}

	rep.OK = len(rep.Missing) == 0
	return rep
}

// checkCallClaim verifies a call-graph claim (caller calls callee) against the
// index. Both sides may be written qualified ("service.FindUser",
// "repo.Query") or bare ("FindUser"); matching accepts the full key or its
// final dotted component. Without an index the claim is unverifiable (F-017).
func checkCallClaim(ix *index.Index, caller, callee string) (bool, string) {
	if ix == nil {
		return false, "no index available for call-graph verification"
	}
	if ix.Calls == nil || ix.Callers == nil {
		return false, "index has no call graph"
	}
	// Caller keys: the claim text plus any local symbols it names.
	callerKeys := nameKeys(ix, caller)
	calleeKeys := nameKeys(ix, callee)
	for _, ck := range callerKeys {
		for _, target := range ix.CallSites(ck) {
			for _, ck2 := range calleeKeys {
				if nameMatches(target, ck2) {
					return true, fmt.Sprintf("call graph: %s calls %s", caller, callee)
				}
			}
		}
	}
	// Also check from the callee side (covers targets recorded under a
	// qualified key the caller lookup missed).
	for _, ck := range calleeKeys {
		for _, c := range ix.CallersOfName(ck) {
			for _, ck2 := range callerKeys {
				if nameMatches(c, ck2) {
					return true, fmt.Sprintf("call graph: %s is called by %s", callee, caller)
				}
			}
		}
	}
	return false, fmt.Sprintf("no call edge: %s does not call %s", caller, callee)
}

// nameKeys returns the lookup keys for a claim name: the text itself plus the
// FullName of every indexed symbol it names (by exact or bare-name match), so
// "repo.Query" and "Query" both resolve to the same recorded edges.
func nameKeys(ix *index.Index, name string) []string {
	keys := []string{name}
	bare := name
	if i := strings.LastIndexByte(bare, '.'); i >= 0 {
		bare = bare[i+1:]
	}
	for _, s := range ix.Symbols {
		if s.FullName() == name || s.Name == name || s.FullName() == bare || s.Name == bare {
			keys = append(keys, s.FullName())
		}
	}
	return keys
}

// nameMatches reports whether a call-graph key (possibly qualified, e.g.
// "db.Open") names the same function as the claim text (possibly qualified
// too, e.g. "service.FindUser"). Matching accepts the full key or its final
// dotted component, so "repo.Query" matches "Query" and vice versa.
func nameMatches(key, name string) bool {
	if key == name {
		return true
	}
	kb := key
	if i := strings.LastIndexByte(kb, '.'); i >= 0 {
		kb = kb[i+1:]
	}
	nb := name
	if i := strings.LastIndexByte(nb, '.'); i >= 0 {
		nb = nb[i+1:]
	}
	return kb == nb && kb != ""
}

// checkFileLine validates whether file exists in root (or ix) and line is within bounds.
func checkFileLine(ix *index.Index, root, file string, line int) (bool, string) {
	if line < 1 {
		return false, fmt.Sprintf("invalid line number %d", line)
	}
	if root == "" && ix != nil && ix.Root != "" {
		root = ix.Root
	}
	path := file
	if filepath.IsAbs(file) {
		if root == "" || !WithinAbs(root, file) {
			return false, "file outside root / inaccessible"
		}
		path = file
	} else if root != "" {
		path = filepath.Join(root, file)
	}

	// If direct path doesn't exist, try resolving via index file list (e.g. bare filename).
	if _, err := os.Stat(path); err != nil && ix != nil {
		target := file
		if filepath.IsAbs(file) && root != "" {
			if rel, err := filepath.Rel(root, file); err == nil {
				target = rel
			}
		}
		var matched string
		for f := range ix.FileHashes {
			if f == target || filepath.Base(f) == target || strings.HasSuffix(f, "/"+target) {
				matched = f
				break
			}
		}
		if matched == "" {
			for _, s := range ix.Symbols {
				if s.File == target || filepath.Base(s.File) == target || strings.HasSuffix(s.File, "/"+target) {
					matched = s.File
					break
				}
			}
		}
		if matched != "" && root != "" {
			path = filepath.Join(root, matched)
		}
	}

	b, err := os.ReadFile(path)
	if err != nil {
		return false, "file not found in source tree"
	}
	totalLines := strings.Count(string(b), "\n") + 1
	if line > totalLines {
		return false, fmt.Sprintf("line %d exceeds file length (%d lines)", line, totalLines)
	}
	return true, "file+line present in source"
}

// WithinAbs reports whether child (absolute) stays inside parent (absolute).
// Unlike mcp's within, it does not reject an absolute rel (e.g. another drive
// root), which mcp's variant guards against.
func WithinAbs(parent, child string) bool {
	rel, err := filepath.Rel(parent, child)
	if err != nil {
		return false
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator))
}

// looksLikeRoute reports whether a slash-path is a plausible route candidate
// rather than noise: paths pointing at (or under an ancestor of) an existing
// filesystem entry and /YYYY/MM/DD date-like paths are not routes. The
// filesystem root "/" and the project root are never statted — they always
// exist and would otherwise hide every absolute route.
func looksLikeRoute(m, root string) bool {
	segs := strings.Split(strings.Trim(m, "/"), "/")
	if len(segs) == 3 && isAllDigits(segs[0]) && isAllDigits(segs[1]) && isAllDigits(segs[2]) {
		return false
	}
	// Reject paths that look like file references: any segment with a file
	// extension (a dot followed by 1-5 alpha chars) is a file path, not a
	// route. This prevents /NotificationListenerRunner.java and similar
	// source-file paths from being misreported as unregistered routes.
	for _, seg := range segs {
		if i := strings.LastIndex(seg, "."); i > 0 {
			ext := seg[i+1:]
			if len(ext) >= 1 && len(ext) <= 5 && isAlphaExt(ext) {
				return false
			}
		}
	}
	// Reject paths that contain common source directory segments (src,
	// main, test, java, com, org, etc.) — these are file paths from
	// import/package statements, not HTTP routes.
	if hasSourcePathSegment(segs) {
		return false
	}
	p := m
	rootAbs := ""
	if root != "" {
		rootAbs, _ = filepath.Abs(root)
	}
	if !filepath.IsAbs(p) && root != "" {
		p = filepath.Join(root, m)
	}
	stops := map[string]bool{"/": true}
	if rootAbs != "" {
		stops[rootAbs] = true
	}
	dir := p
	for {
		if stops[dir] {
			break
		}
		if _, err := os.Stat(dir); err == nil {
			return false
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	return true
}

// isAlphaExt reports whether s is a short all-alpha file extension (java, py,
// go, ts, etc.) — used to distinguish file paths from route paths.
func isAlphaExt(s string) bool {
	for _, r := range s {
		if !((r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
	}
	return true
}

// sourcePathSegments are directory names that are unambiguously part of a
// source tree, not an HTTP route. Their presence in a slash-path means the
// path is a file/import path, not a route.
var sourcePathSegments = map[string]bool{
	"src": true, "main": true, "test": true, "tests": true,
	"java": true, "kotlin": true, "scala": true, "groovy": true,
	"resources": true, "lib": true, "libs": true, "pkg": true,
	"internal": true, "cmd": true, "include": true, "includes": true,
	"shaders": true,
}

// hasSourcePathSegment reports whether any segment of the path is a known
// source directory name.
func hasSourcePathSegment(segs []string) bool {
	for _, seg := range segs {
		if sourcePathSegments[seg] {
			return true
		}
	}
	return false
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

func isExported(s string) bool {
	return len(s) > 0 && s[0] >= 'A' && s[0] <= 'Z'
}

// Sorted returns a copy of rep with checks in a deterministic order.
func Sorted(rep Report) Report {
	out := Report{Checks: append([]Check(nil), rep.Checks...), Missing: append([]string(nil), rep.Missing...), OK: rep.OK}
	sort.Slice(out.Checks, func(i, j int) bool {
		if out.Checks[i].Type != out.Checks[j].Type {
			return out.Checks[i].Type < out.Checks[j].Type
		}
		return out.Checks[i].Ref < out.Checks[j].Ref
	})
	return out
}

// Render formats a report as human-readable lines.
func Render(rep Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "verified %d references:\n", len(rep.Checks))
	for _, c := range rep.Checks {
		mark := "ok  "
		if !c.Found {
			mark = "MISS"
		}
		fmt.Fprintf(&b, "  [%s] %-9s %-30s %s\n", mark, c.Type, c.Ref, c.Detail)
	}
	if rep.OK {
		b.WriteString("all references confirmed against the source tree")
	} else {
		fmt.Fprintf(&b, "%d unverifiable/missing references", len(rep.Missing))
	}
	return strings.TrimSuffix(b.String(), "\n")
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
