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
		add(FileRef, file+":"+m[2], fileHasLine(root, file, line), "file+line present in source")
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
					add(Sym, m, true, "indexed at "+s.File+":"+itoa(s.Line))
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
	// (or paths under an existing ancestor) and date-like /YYYY/MM/DD
	// (W2-30).
	for _, m := range routeRe.FindAllString(text, -1) {
		m = strings.TrimRight(m, ".,;:)!?]}")
		if m == "" || !looksLikeRoute(m, root) {
			continue
		}
		if !seen["route|"+m] {
			add(Route, m, false, "no indexed handler registers this route")
		}
	}

	rep.OK = len(rep.Missing) == 0
	return rep
}

// fileHasLine reports whether file exists in root and line is within bounds.
// Absolute file refs are only honored when they stay inside root, so
// untrusted text can never probe arbitrary machine paths (W2-32).
func fileHasLine(root, file string, line int) bool {
	if line < 1 {
		return false
	}
	path := file
	if root != "" && !filepath.IsAbs(file) {
		path = filepath.Join(root, file)
	}
	if filepath.IsAbs(path) {
		if root == "" || !withinAbs(root, path) {
			return false
		}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	return line <= strings.Count(string(b), "\n")+1
}

// withinAbs reports whether child (absolute) stays inside parent (absolute).
func withinAbs(parent, child string) bool {
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
// exist and would otherwise hide every absolute route (W2-30).
func looksLikeRoute(m, root string) bool {
	segs := strings.Split(strings.Trim(m, "/"), "/")
	if len(segs) == 3 && isAllDigits(segs[0]) && isAllDigits(segs[1]) && isAllDigits(segs[2]) {
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

func itoa(n int) string {
	return strconv.Itoa(n)
}
