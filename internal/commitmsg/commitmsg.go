// Package commitmsg derives a deterministic commit message from a unified
// diff. It is intentionally rule-based (no LLM, no network): the type, scope
// and subject are inferred from changed file paths and added/removed lines, so
// the same diff always produces the same message and a human can tweak it.
package commitmsg

import (
	"strconv"
	"strings"
)

// Message is a generated commit message.
type Message struct {
	Type    string // fix, feat, refactor, docs, test, chore
	Scope   string
	Subject string
	Body    []string
}

// String renders the conventional-commit form: "type(scope): subject" plus an
// empty line and one bullet per changed file.
func (m Message) String() string {
	var b strings.Builder
	b.WriteString(m.Subject)
	b.WriteString("\n")
	if len(m.Body) > 0 {
		b.WriteString("\n")
		for _, l := range m.Body {
			b.WriteString(l)
			b.WriteString("\n")
		}
	}
	return strings.TrimSuffix(b.String(), "\n")
}

var fixWords = map[string]bool{
	"fix": true, "fixes": true, "fixed": true, "bug": true, "error": true,
	"crash": true, "panic": true, "regression": true, "broken": true,
	"incorrect": true, "wrong": true, "fails": true, "failed": true,
	"failure": true, "issue": true, "prevent": true, "avoid": true,
	"guard": true, "revert": true, "leak": true,
}

var featWords = map[string]bool{
	"add": true, "adds": true, "added": true, "new": true, "support": true,
	"supports": true, "implement": true, "implements": true, "feature": true,
	"introduce": true, "introduces": true, "enable": true, "enables": true,
	"allow": true, "allows": true, "expose": true, "wire": true, "render": true,
}

var refactorWords = map[string]bool{
	"refactor": true, "refactors": true, "move": true, "moves": true,
	"rename": true, "renames": true, "extract": true, "restructure": true,
	"simplify": true, "simplifies": true, "clean": true, "cleanup": true,
	"split": true, "merge": true, "consolidate": true, "dedupe": true,
	"reduce": true, "improve": true,
}

var docsWords = map[string]bool{
	"docs": true, "document": true, "documentation": true, "readme": true,
	"comment": true, "comments": true, "guide": true, "example": true,
}

var testWords = map[string]bool{
	"test": true, "tests": true, "tested": true, "spec": true, "assert": true,
	"fixture": true, "coverage": true,
}

var docExts = map[string]bool{
	".md": true, ".txt": true, ".rst": true, ".adoc": true, ".asciidoc": true,
}

type fileChange struct {
	path    string
	added   []string
	removed []string
	renamed bool
}

// Generate parses a unified diff (as produced by `git diff`) and returns a
// deterministic message. An empty or unparseable diff yields a chore subject.
func Generate(diffText string) Message {
	files := parseDiff(diffText)
	if len(files) == 0 {
		return Message{Type: "chore", Subject: "chore: update"}
	}

	typ := classify(files)
	scope := scopeOf(files)
	noun := subjectNoun(files)
	subject := typ
	if scope != "" {
		subject += "(" + scope + ")"
	}
	subject += ": "
	if noun != "" {
		subject += noun
	} else {
		subject += "update"
	}

	var body []string
	for _, f := range files {
		note := classifyNote(f)
		line := "- " + f.path
		if f.renamed {
			line += " (renamed)"
		}
		line += " (" + itoa(len(f.added)) + "+," + itoa(len(f.removed)) + "-)"
		if note != "" {
			line += " " + note
		}
		body = append(body, line)
	}
	return Message{Type: typ, Scope: scope, Subject: subject, Body: body}
}

// parseDiff walks the unified diff keeping only file headers, rename markers
// and added/removed lines, grouped per file.
func parseDiff(d string) []fileChange {
	var files []fileChange
	var cur *fileChange
	push := func() {
		if cur != nil {
			files = append(files, *cur)
		}
		cur = nil
	}
	for _, l := range strings.Split(d, "\n") {
		switch {
		case strings.HasPrefix(l, "diff --git "):
			push()
			// Header: `diff --git a/old b/new`. When core.quotePath kicks in
			// (spaces, non-ASCII) git C-quotes each side: `diff --git
			// "a/old file" "b/new file"`, so a fixed-offset slice at a/ breaks.
			p := strings.TrimSpace(l[len("diff --git "):])
			if strings.HasPrefix(p, `"`) {
				// Find the end of the C-quoted first operand, honoring
				// backslash escapes, then unquote it in full.
				end := len(p)
				for i := 1; i < len(p); i++ {
					if p[i] == '\\' {
						i++
						continue
					}
					if p[i] == '"' {
						end = i + 1
						break
					}
				}
				if q, err := strconv.Unquote(p[:end]); err == nil {
					p = q
				}
			} else if i := strings.LastIndex(p, " b/"); i >= 0 {
				// The from path ends at the last " b/" (right-most, so a path
				// containing " b/" is not split).
				p = p[:i]
			}
			p = strings.TrimPrefix(p, "a/")
			cur = &fileChange{path: unquoteGitPath(p)}
		case cur == nil:
			continue
		case strings.HasPrefix(l, "rename from "):
			cur.renamed = true
		case strings.HasPrefix(l, "rename to "):
			cur.renamed = true
			cur.path = unquoteGitPath(strings.TrimSpace(strings.TrimPrefix(l, "rename to ")))
		case strings.HasPrefix(l, "+++ ") || strings.HasPrefix(l, "--- "):
			continue
		case strings.HasPrefix(l, "@@"):
			continue
		case strings.HasPrefix(l, "+"):
			cur.added = append(cur.added, strings.TrimPrefix(l, "+"))
		case strings.HasPrefix(l, "-"):
			cur.removed = append(cur.removed, strings.TrimPrefix(l, "-"))
		}
	}
	push()
	return files
}

// unquoteGitPath undoes git's C-quoting of a path header (`"my file.txt"`),
// returning the literal path. Unquoted paths pass through unchanged.
func unquoteGitPath(p string) string {
	p = strings.TrimSpace(p)
	if strings.HasPrefix(p, `"`) {
		if u, err := strconv.Unquote(p); err == nil {
			return u
		}
	}
	return p
}

func isDoc(path string) bool {
	i := strings.LastIndex(path, ".")
	if i < 0 {
		return false
	}
	return docExts[strings.ToLower(path[i:])]
}

func isTestFile(path string) bool {
	p := strings.ToLower(path)
	return strings.Contains(p, "_test.") || strings.Contains(p, "/test/") || strings.HasPrefix(p, "test/")
}

func countHits(lines []string, words map[string]bool) int {
	hits := 0
	for _, l := range lines {
		for _, w := range tokenizeWords(l) {
			if words[w] {
				hits++
			}
		}
	}
	return hits
}

// classify picks the commit message. When the change adds top-level Go
// declarations (col-0 func/type/var/const), the type is driven by those
// declarations alone — a new exported symbol is a feature, and incidental
// fix keywords in surrounding body lines can no longer outvote it. Without
// added declarations, it falls back to fix/feat/refactor keyword hits over
// the added lines (highest score wins, fix first for ties). A change touching
// only tests is "test" and only docs is "docs"; otherwise "chore". Doc/test
// type is decided by file kind, never by content keywords, so prose mentioning
// "docs" cannot outvote a code change.
func classify(files []fileChange) string {
	var codeFiles, testFiles, docFiles []fileChange
	for _, f := range files {
		switch {
		case isTestFile(f.path):
			testFiles = append(testFiles, f)
		case isDoc(f.path):
			docFiles = append(docFiles, f)
		default:
			codeFiles = append(codeFiles, f)
		}
	}
	if len(codeFiles) == 0 {
		if len(testFiles) > 0 {
			return "test"
		}
		if len(docFiles) > 0 {
			return "docs"
		}
		return "chore"
	}

	// Declaration-aware path: the added top-level declarations are the signal,
	// not the incidental keywords in modified bodies.
	if decls, lines := codeAddedDeclarations(codeFiles); len(decls) > 0 {
		for _, d := range decls {
			if isExported(d) {
				return "feat" // new public API is the strongest feature signal
			}
		}
		best, bestScore := "chore", 0
		for _, sig := range []struct {
			typ   string
			words map[string]bool
		}{
			{"feat", featWords}, {"fix", fixWords}, {"refactor", refactorWords},
		} {
			if s := countHits(lines, sig.words); s > bestScore {
				best, bestScore = sig.typ, s
			}
		}
		if bestScore > 0 {
			return best
		}
		// New top-level symbols with no decisive keyword footprint: additive
		// new code.
		return "feat"
	}

	scores := map[string]int{"fix": 0, "feat": 0, "refactor": 0}
	for _, f := range codeFiles {
		scores["fix"] += countHits(f.added, fixWords)
		scores["feat"] += countHits(f.added, featWords)
		scores["refactor"] += countHits(f.added, refactorWords)
	}
	priority := []string{"fix", "feat", "refactor"}
	best, bestScore := "chore", 0
	for _, typ := range priority {
		if scores[typ] > bestScore {
			best, bestScore = typ, scores[typ]
		}
	}
	return best
}

// codeAddedDeclarations collects the added top-level declarations across the
// code files (names, preserving case) and their source lines, so classification
// can weight declarations instead of arbitrary added body lines.
func codeAddedDeclarations(codeFiles []fileChange) (names []string, lines []string) {
	for _, f := range codeFiles {
		for _, l := range f.added {
			if n, ok := declOf(l); ok {
				names = append(names, n)
				lines = append(lines, l)
			}
		}
	}
	return names, lines
}

// declOf reports whether a line begins a top-level Go declaration (at column 0,
// so an indented function/local is ignored) and returns the declared symbol's
// name preserving case. Handles func, methods (func (r *Recv) Name), type, var
// and const.
func declOf(line string) (string, bool) {
	if line == "" {
		return "", false
	}
	switch line[0] {
	case ' ', '\t':
		return "", false // indented → not a top-level declaration
	}
	var rest string
	switch {
	case strings.HasPrefix(line, "func "):
		rest = strings.TrimSpace(strings.TrimPrefix(line, "func "))
		if strings.HasPrefix(rest, "(") {
			rest = trimAfterReceiver(rest) // method: drop the receiver, keep Name
		}
	case strings.HasPrefix(line, "type "):
		rest = strings.TrimSpace(strings.TrimPrefix(line, "type "))
	case strings.HasPrefix(line, "var "):
		rest = strings.TrimSpace(strings.TrimPrefix(line, "var "))
	case strings.HasPrefix(line, "const "):
		rest = strings.TrimSpace(strings.TrimPrefix(line, "const "))
	default:
		return "", false
	}
	name := firstIdent(rest)
	if name == "" {
		return "", false
	}
	return name, true
}

// trimAfterReceiver consumes a leading "(" ... ")" receiver and returns what
// follows (the method name).
func trimAfterReceiver(s string) string {
	if !strings.HasPrefix(s, "(") {
		return s
	}
	depth := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '(':
			depth++
		case ')':
			depth--
			if depth == 0 {
				return strings.TrimSpace(s[i+1:])
			}
		}
	}
	return s
}

// firstIdent returns the first identifier in s (preserving case), or "".
func firstIdent(s string) string {
	for i := 0; i < len(s); i++ {
		if isIdentStart(s[i]) {
			j := i + 1
			for j < len(s) && isIdentChar(s[j]) {
				j++
			}
			return s[i:j]
		}
	}
	return ""
}

func isIdentStart(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

func isIdentChar(c byte) bool {
	return isIdentStart(c) || (c >= '0' && c <= '9')
}

// isExported reports whether name is an exported Go identifier (uppercase).
func isExported(name string) bool {
	return name != "" && name[0] >= 'A' && name[0] <= 'Z'
}

// isTestFunc reports whether name is a Go test/benchmark/example function that
// should not name a commit's subject.
func isTestFunc(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Example") ||
		strings.HasPrefix(name, "Fuzz")
}

// scopeOf is the common directory prefix of the changed files, with generic
// container segments (src, internal, pkg, cmd, lib) stripped but never to
// nothing.
func scopeOf(files []fileChange) string {
	var segs [][]string
	for _, f := range files {
		segs = append(segs, strings.Split(filepathSlash(f.path), "/"))
	}
	if len(segs) == 0 {
		return ""
	}
	common := segs[0]
	for _, s := range segs[1:] {
		n := 0
		for n < len(common) && n < len(s) && common[n] == s[n] {
			n++
		}
		common = common[:n]
	}
	// A single file pulls its own name into the prefix; drop it to get dirs.
	if len(common) > 0 && strings.Contains(common[len(common)-1], ".") {
		common = common[:len(common)-1]
	}
	if len(common) == 0 {
		return ""
	}
	generic := map[string]bool{"src": true, "internal": true, "pkg": true, "cmd": true, "lib": true, "app": true}
	dirs := common
	for len(dirs) > 0 && generic[dirs[0]] {
		dirs = dirs[1:]
	}
	if len(dirs) == 0 {
		dirs = common[len(common)-1:] // keep at least the deepest dir
	}
	if len(dirs) > 2 {
		dirs = dirs[len(dirs)-2:]
	}
	return strings.Join(dirs, "/")
}

// subjectNoun picks the strongest noun from the added lines: the first added
// top-level declaration's identifier (the new symbol the commit introduces),
// skipping Go test/benchmark/example functions; else the first
// function/method identifier, else the first identifier token.
func subjectNoun(files []fileChange) string {
	for _, f := range files {
		for _, l := range f.added {
			if n, ok := declOf(l); ok && !isTestFunc(n) {
				return strings.ToLower(n)
			}
		}
	}
	for _, f := range files {
		for _, l := range f.added {
			trimmed := strings.TrimSpace(l)
			if trimmed == "" {
				continue
			}
			if id := declIdent(trimmed); id != "" {
				return strings.ToLower(id)
			}
		}
	}
	for _, f := range files {
		for _, l := range f.added {
			for _, w := range tokenizeWords(l) {
				if !isStopWord(w) {
					return w
				}
			}
		}
	}
	return ""
}

// declIdent returns the identifier immediately before the first "(" of a line,
// skipping a Go method receiver so `func (r *Recv) Name(...)` yields Name
// rather than func.
func declIdent(trimmed string) string {
	s := strings.TrimSpace(trimmed)
	if strings.HasPrefix(s, "func") {
		s = strings.TrimSpace(strings.TrimPrefix(s, "func"))
		if strings.HasPrefix(s, "(") {
			depth := 0
			for i := 0; i < len(s); i++ {
				switch s[i] {
				case '(':
					depth++
				case ')':
					depth--
					if depth == 0 {
						s = strings.TrimSpace(s[i+1:])
						i = len(s) // receiver consumed
					}
				}
			}
		}
	}
	if i := strings.Index(s, "("); i > 0 {
		id := lastIdent(s[:i])
		if id != "" {
			return id
		}
	}
	return ""
}

func lastIdent(s string) string {
	words := tokenizeWords(s)
	if len(words) == 0 {
		return ""
	}
	return words[len(words)-1]
}

func classifyNote(f fileChange) string {
	// Prefer the top-level declaration the file adds, so the note names the
	// introduced symbol instead of grabbing two random tokens from a body line.
	for _, l := range f.added {
		if n, ok := declOf(l); ok && !isTestFunc(n) {
			return "add " + strings.ToLower(n)
		}
	}
	words := tokenizeWords(strings.Join(f.added, " "))
	if len(words) == 0 {
		return ""
	}
	for i := 0; i < len(words); i++ {
		if isChangeWord(words[i]) && i+1 < len(words) && !isStopWord(words[i+1]) {
			return words[i] + " " + words[i+1]
		}
	}
	return ""
}

func isChangeWord(w string) bool {
	return fixWords[w] || featWords[w] || refactorWords[w] || docsWords[w] || testWords[w]
}

func tokenizeWords(s string) []string {
	var out []string
	var b strings.Builder
	flush := func() {
		if b.Len() > 0 {
			out = append(out, strings.ToLower(b.String()))
			b.Reset()
		}
	}
	for _, r := range s {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '_' {
			b.WriteRune(r)
		} else {
			flush()
		}
	}
	flush()
	return out
}

var stopWords = map[string]bool{
	"a": true, "an": true, "the": true, "to": true, "of": true, "in": true,
	"on": true, "for": true, "and": true, "or": true, "is": true, "are": true,
	"be": true, "with": true, "this": true, "that": true, "from": true,
	"by": true, "it": true, "as": true, "at": true, "we": true, "our": true,
	"if": true, "not": true, "no": true, "do": true, "var": true, "const": true,
	"func": true, "return": true, "package": true, "import": true, "nil": true,
	"err": true, "error": true, "bool": true, "int": true, "string": true,
}

func isStopWord(w string) bool { return stopWords[w] }

func filepathSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
