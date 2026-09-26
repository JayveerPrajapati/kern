// Package commitmsg derives a deterministic commit message from a unified
// diff. It is intentionally rule-based (no LLM, no network): the type, scope
// and subject are inferred from changed file paths and added/removed lines, so
// the same diff always produces the same message and a human can tweak it.
package commitmsg

import (
	"strconv"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/code"
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

// typeScoreOrder is the keyword-scoring priority: within one scoring pass the
// first type whose hits exceed the running best wins, so a tie on a single
// pass still prefers the feature reading.
var typeScoreOrder = []struct {
	typ   string
	words map[string]bool
}{
	{"feat", featWords},
	{"fix", fixWords},
	{"refactor", refactorWords},
}

type fileChange struct {
	path    string
	added   []string
	removed []string
	renamed bool
	// declHits counts changed (+/-) lines attributed to each enclosing
	// top-level declaration, tracked while parsing (the declaration header
	// itself may come from a hunk CONTEXT line). This is the "what was
	// modified" signal: subjectNoun prefers it over naive word grabs.
	// curDecl is the parser's current enclosing declaration.
	declHits map[string]int
	curDecl  string
	// addedAt holds the NEW-side line number of each added line (parallel to
	// added), tracked from the @@ hunk headers. Enhance uses it to attribute
	// added lines to the enclosing declaration by reading the file itself —
	// body-deep edits far from any hunk-context header still resolve.
	// newLine is the parser's running NEW-side position.
	addedAt []int
	newLine int
}

// Generate parses a unified diff (as produced by `git diff`) and returns a
// deterministic message. An empty or unparseable diff yields a chore subject.
func Generate(diffText string) Message {
	return buildMessage(parseDiff(diffText))
}

// Enhance is Generate with file access: when read can supply a changed file's
// CURRENT content, added lines are attributed to their enclosing top-level
// declaration by line number — so a body-deep edit names the function it
// modified, not a word grabbed from the added lines (F-CM1). Files read
// returns false for are simply left to the diff-only heuristics.
func Enhance(diffText string, read func(path string) (string, bool)) Message {
	files := parseDiff(diffText)
	for i := range files {
		content, ok := read(files[i].path)
		if !ok {
			continue
		}
		attributeByLineNumbers(&files[i], content)
	}
	return buildMessage(files)
}

// attributeByLineNumbers maps each added line's NEW-side number to the
// enclosing top-level declaration of the file content, feeding declHits.
// A declaration starts at any column-0 func/type/var/const line and runs to
// the next such line (or EOF).
func attributeByLineNumbers(f *fileChange, content string) {
	// Collect top-level declaration start lines.
	lines := strings.Split(content, "\n")
	declAt := make([]int, 0, 8)
	declName := make([]string, 0, 8)
	for i, l := range lines {
		if l == "" || (l[0] != 'f' && l[0] != 't' && l[0] != 'v' && l[0] != 'c') {
			continue
		}
		if id := declIdent(l); id != "" && !isTestFunc(id) {
			declAt = append(declAt, i+1) // 1-based
			declName = append(declName, id)
		}
	}
	if len(declAt) == 0 {
		return
	}
	if f.declHits == nil {
		f.declHits = map[string]int{}
	}
	for _, ln := range f.addedAt {
		if ln <= 0 {
			continue
		}
		// Find the last declaration starting at or before ln.
		lo, hi := 0, len(declAt)-1
		for lo < hi {
			mid := (lo + hi + 1) / 2
			if declAt[mid] <= ln {
				lo = mid
			} else {
				hi = mid - 1
			}
		}
		if declAt[lo] <= ln {
			f.declHits[declName[lo]]++
		}
	}
}

// hunkNewStart extracts the NEW-side start line from a unified-diff hunk
// header "@@ -a,b +c,d @@ ..." (c > 0 on success).
func hunkNewStart(header string) int {
	plus := strings.Index(header, "+")
	if plus < 0 {
		return 0
	}
	rest := header[plus+1:]
	end := strings.IndexAny(rest, ", @")
	if end < 0 {
		end = len(rest)
	}
	n, err := strconv.Atoi(strings.TrimSpace(rest[:end]))
	if err != nil || n < 0 {
		return 0
	}
	return n
}

// buildMessage is Generate's core over parsed files.
func buildMessage(files []fileChange) Message {
	// Exclude VCS/build/vendor-dir noise (vendor/, dist/, ...) so a
	// vendor-heavy diff cannot drown the message in boilerplate.
	if len(files) > 0 {
		clean := files[:0]
		for _, f := range files {
			if !code.ShouldIgnore(f.path) {
				clean = append(clean, f)
			}
		}
		files = clean
	}
	if len(files) == 0 {
		return Message{Type: "chore", Subject: "chore: update"}
	}

	// A change is single-area when every file shares at least the first path
	// segment (a shared "cmd" or "internal" segment counts). Cross-cutting
	// commits lose the exported-declaration headline rules: no one package
	// owns them, so one exported symbol or one error sentinel cannot speak for
	// the whole diff.
	singleArea := commonPrefixNonEmpty(files)
	typ := classify(files, singleArea)
	scope := scopeOf(files)
	noun := subjectNoun(files, singleArea)
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
		line += " (" + strconv.Itoa(len(f.added)) + "+," + strconv.Itoa(len(f.removed)) + "-)"
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
			// Hunk header: `@@ -a,b +c,d @@` — c is the first NEW-side line
			// number. Context and added lines advance it; removed lines do
			// not.
			if c := hunkNewStart(l); c > 0 {
				cur.newLine = c
			}
		case strings.HasPrefix(l, "+"):
			body := strings.TrimPrefix(l, "+")
			cur.added = append(cur.added, body)
			cur.addedAt = append(cur.addedAt, cur.newLine)
			if cur.newLine > 0 {
				cur.newLine++
			}
			attributeDecl(cur, body)
		case strings.HasPrefix(l, " "):
			// Context lines update the enclosing-declaration tracker so
			// +/- lines deep inside a small declaration are still attributed
			// to it (the header line itself often only appears as context).
			if cur.newLine > 0 {
				cur.newLine++
			}
			attributeDecl(cur, strings.TrimPrefix(l, " "))
		case strings.HasPrefix(l, "-"):
			body := strings.TrimPrefix(l, "-")
			cur.removed = append(cur.removed, body)
			attributeDecl(cur, body)
		}
	}
	push()
	return files
}

// attributeDecl updates the enclosing-declaration tracker for a file: a
// top-level declaration header (func/type/var/const) becomes the current
// decl, and changed lines increment the hit count of the decl they land in.
// The header itself may come from a hunk CONTEXT line — that is the point:
// body-only edits deep inside a small declaration stay attributed to it.
func attributeDecl(f *fileChange, line string) {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return
	}
	if id := declIdent(trimmed); id != "" && !isTestFunc(id) {
		f.curDecl = id
		return
	}
	if f.curDecl == "" {
		return
	}
	if f.declHits == nil {
		f.declHits = map[string]int{}
	}
	f.declHits[f.curDecl]++
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
// declarations alone: in a single-area change a new exported symbol (other
// than an Err* error sentinel, which is fix infrastructure) is a feature, and
// incidental fix keywords in surrounding body lines can no longer outvote it.
// A cross-cutting (multi-area) change never becomes feat on the strength of
// one exported symbol among many packages: its declaration lines are
// keyword-scored, falling back to all added code lines when the declarations
// alone carry no signal, and only then to the additive-feat default. Without
// added declarations, it falls back to fix/feat/refactor keyword hits over
// the added lines (highest score wins, fix first for ties). A change touching
// only tests is "test" and only docs is "docs"; otherwise "chore". Doc/test
// type is decided by file kind, never by content keywords, so prose mentioning
// "docs" cannot outvote a code change.
func classify(files []fileChange, singleArea bool) string {
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
		if singleArea {
			for _, d := range decls {
				if isExported(d) {
					return "feat" // new public API is the strongest feature signal
				}
			}
		}
		best, bestScore := "chore", 0
		for _, sig := range typeScoreOrder {
			if s := countHits(lines, sig.words); s > bestScore {
				best, bestScore = sig.typ, s
			}
		}
		if bestScore == 0 && !singleArea {
			// Cross-cutting change: no decisive keyword footprint on the
			// declaration lines alone, so score every added code line before
			// falling back to the additive default.
			var all []string
			for _, f := range codeFiles {
				all = append(all, f.added...)
			}
			for _, sig := range typeScoreOrder {
				if s := countHits(all, sig.words); s > bestScore {
					best, bestScore = sig.typ, s
				}
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
// can weight declarations instead of arbitrary added body lines. Err*-prefixed
// declarations (error sentinels) are fix infrastructure and are excluded: they
// must never drive a commit to feat, nor leak their "new"/"add" tokens into the
// declaration keyword scoring.
func codeAddedDeclarations(codeFiles []fileChange) (names []string, lines []string) {
	for _, f := range codeFiles {
		for _, l := range f.added {
			if n, ok := declOf(l); ok && !isErrSentinel(n) {
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

// isErrSentinel reports whether name is an Err*-prefixed Go error sentinel or
// error type. Such declarations are fix infrastructure, never a feature
// headline, and must not name a commit's subject on their own.
func isErrSentinel(name string) bool {
	return strings.HasPrefix(name, "Err")
}

// isTestFunc reports whether name is a Go test/benchmark/example function that
// should not name a commit's subject.
func isTestFunc(name string) bool {
	return strings.HasPrefix(name, "Test") ||
		strings.HasPrefix(name, "Benchmark") ||
		strings.HasPrefix(name, "Example") ||
		strings.HasPrefix(name, "Fuzz")
}

// commonPathSegments returns the path segments shared by every file, in order.
// It is empty when the files diverge at the first segment. A single file's
// prefix includes its own file-name segment.
func commonPathSegments(files []fileChange) []string {
	if len(files) == 0 {
		return nil
	}
	common := strings.Split(filepathSlash(files[0].path), "/")
	for _, f := range files[1:] {
		s := strings.Split(filepathSlash(f.path), "/")
		n := 0
		for n < len(common) && n < len(s) && common[n] == s[n] {
			n++
		}
		common = common[:n]
	}
	return common
}

// commonPrefixNonEmpty reports whether every file shares at least the first
// path segment — the change stays within a single top-level area. A shared
// generic segment (cmd, internal, ...) counts, mirroring how scopeOf treats
// generic segments.
func commonPrefixNonEmpty(files []fileChange) bool {
	return len(commonPathSegments(files)) > 0
}

// scopeOf is the common directory prefix of the changed files, with generic
// container segments (src, internal, pkg, cmd, lib) stripped but never to
// nothing. When the files share no prefix at all, the fallback scope is the
// dominant file's package directory — but only when that file really owns the
// diff (≥ 50% of all changed lines); a cross-cutting change gets no scope.
func scopeOf(files []fileChange) string {
	if len(files) == 0 {
		return ""
	}
	common := commonPathSegments(files)
	// A single file pulls its own name into the prefix; drop it to get dirs.
	if len(common) > 0 && strings.Contains(common[len(common)-1], ".") {
		common = common[:len(common)-1]
	}
	generic := map[string]bool{"src": true, "internal": true, "pkg": true, "cmd": true, "lib": true, "app": true}
	if len(common) == 0 {
		// Fallback: pick the primary package directory from the file with the
		// most changed lines, but only when that file dominates the diff.
		bestFile := files[0]
		maxLines := len(bestFile.added) + len(bestFile.removed)
		total := 0
		for _, f := range files {
			total += len(f.added) + len(f.removed)
			if lines := len(f.added) + len(f.removed); lines > maxLines {
				maxLines = lines
				bestFile = f
			}
		}
		if maxLines*2 < total {
			return ""
		}
		parts := strings.Split(filepathSlash(bestFile.path), "/")
		if len(parts) > 1 {
			dirs := parts[:len(parts)-1]
			for len(dirs) > 0 && generic[dirs[0]] {
				dirs = dirs[1:]
			}
			if len(dirs) > 0 {
				if len(dirs) > 2 {
					dirs = dirs[len(dirs)-2:]
				}
				return strings.Join(dirs, "/")
			}
		}
		return ""
	}
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

// subjectNoun picks the strongest noun from the added lines. In a single-area
// change it is the first added top-level declaration's identifier (the new
// symbol the commit introduces), skipping Go test/benchmark/example functions
// and Err* sentinels. In a multi-area change no single package owns the commit,
// so the exported-declaration step is skipped in favor of the first qualifying
// quoted string literal (e.g. "invalid patch"). Otherwise it falls through to
// the first function/method identifier, then action phrases, then the first
// declaration, then the first identifier token.
// topModifiedDecl returns the declaration absorbing the most changed lines
// across the diff (see fileChange.declHits) and its hit count. Used by
// subjectNoun rule 1.5 (F-CM1): the touched declaration is the subject.
func topModifiedDecl(files []fileChange) (string, int) {
	best, hits := "", 0
	for _, f := range files {
		for id, n := range f.declHits {
			if n > hits && !isStopWord(id) && !isErrSentinel(id) {
				best, hits = id, n
			}
		}
	}
	return best, hits
}

func subjectNoun(files []fileChange, singleArea bool) string {
	if singleArea {
		// 1. Exported declarations take highest precedence (Err sentinels are
		// fix infrastructure and are skipped).
		for _, f := range files {
			for _, l := range f.added {
				if n, ok := declOf(l); ok && isExported(n) && !isTestFunc(n) && !isErrSentinel(n) {
					return splitIdent(n)
				}
			}
		}
	} else {
		// Multi-area: the strongest noun is the first qualifying string
		// literal in the added lines.
		if s := quotedLiteralSubject(files); s != "" {
			return s
		}
	}
	// 1.5 (F-CM1) Modified declarations: when +/- lines concentrate inside
	// an existing declaration (tracked via hunk context headers), THAT
	// declaration is what the commit touched — a far more informative
	// headline than any word grabbed from the added lines. Ranked by hit
	// count so the dominant edit wins; only declarations with real weight
	// (>= 2 changed lines) qualify, so a stray context line cannot
	// headline a fix.
	if best, hits := topModifiedDecl(files); hits >= 2 {
		return splitIdent(best)
	}
	// 2. Added call or method identifiers
	for _, f := range files {
		for _, l := range f.added {
			trimmed := strings.TrimSpace(l)
			if trimmed == "" {
				continue
			}
			if id := declIdent(trimmed); id != "" && !isStopWord(id) {
				return splitIdent(id)
			}
		}
	}
	// 3. Action phrases (e.g. "add support", "prevent blocking", "optimize performance")
	for _, f := range files {
		words := tokenizeWords(strings.Join(f.added, " "))
		for i := 0; i < len(words); i++ {
			if isChangeWord(words[i]) && i+1 < len(words) && !isStopWord(words[i+1]) {
				return words[i] + " " + words[i+1]
			}
		}
	}
	// 4. Any added declaration
	for _, f := range files {
		for _, l := range f.added {
			if n, ok := declOf(l); ok && !isTestFunc(n) {
				return splitIdent(n)
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

// quotedLiteralSubject scans all files' added lines in order for the first
// double-quoted string literal whose tokenized content has at least two
// non-stopword tokens, returning those tokens joined with spaces (capped at
// three). Only top-level (column-0), non-comment lines are scanned: indented
// body lines and comments are skipped so incidental format strings
// (fmt.Fprintf(os.Stderr, "kern: probing %s ...\n", ...)) or prose inside
// comments never hijack a cross-cutting subject.
func quotedLiteralSubject(files []fileChange) string {
	for _, f := range files {
		for _, l := range f.added {
			if l == "" || l[0] == ' ' || l[0] == '\t' {
				continue // indented body line
			}
			if strings.HasPrefix(l, "//") {
				continue // comment, not code
			}
			lit, ok := firstQuotedLiteral(l)
			if !ok {
				continue
			}
			toks := tokenizeWords(lit)
			nonStop := 0
			for _, t := range toks {
				if !isStopWord(t) {
					nonStop++
				}
			}
			if nonStop < 2 {
				continue
			}
			if len(toks) > 3 {
				toks = toks[:3]
			}
			return strings.Join(toks, " ")
		}
	}
	return ""
}

// firstQuotedLiteral returns the text of the first double-quoted string
// literal on a line, honoring backslash escapes so `\"` does not terminate the
// literal early.
func firstQuotedLiteral(line string) (string, bool) {
	i := strings.IndexByte(line, '"')
	if i < 0 {
		return "", false
	}
	var b strings.Builder
	for j := i + 1; j < len(line); j++ {
		switch line[j] {
		case '\\':
			j++ // skip the escaped character
		case '"':
			return b.String(), true
		default:
			b.WriteByte(line[j])
		}
	}
	return "", false
}

// splitIdent splits a Go identifier into space-separated lowercased words on
// camelCase transitions and underscore runs: "ErrInvalidPatch" → "err invalid
// patch", "environmentFor" → "environment for", "NewServer" → "new server",
// "g27RequireKern" → "g27 require kern". An identifier without boundaries
// passes through lowercased.
func splitIdent(name string) string {
	var words []string
	var b strings.Builder
	lastLower := false // previous written rune was lowercase or digit
	flush := func() {
		if b.Len() > 0 {
			words = append(words, b.String())
			b.Reset()
		}
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		switch {
		case c == '_':
			flush() // underscore run → word boundary
			lastLower = false
		case c >= 'A' && c <= 'Z':
			if lastLower {
				flush() // camelCase boundary after a lowercase/digit run
			}
			b.WriteByte(c)
			lastLower = false
		default:
			b.WriteByte(c)
			lastLower = c >= 'a' && c <= 'z' || c >= '0' && c <= '9'
		}
	}
	flush()
	return strings.ToLower(strings.Join(words, " "))
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

// lastIdent returns the last identifier in s, preserving case, so a caller can
// still split camelCase (e.g. environmentFor) before lowercasing for display.
func lastIdent(s string) string {
	for i := len(s) - 1; i >= 0; i-- {
		if isIdentChar(s[i]) {
			j := i
			for j > 0 && isIdentChar(s[j-1]) {
				j--
			}
			return s[j : i+1]
		}
	}
	return ""
}

func classifyNote(f fileChange) string {
	// Prefer the top-level declaration the file adds, so the note names the
	// introduced symbol instead of grabbing two random tokens from a body line.
	// Err* sentinels are skipped: they are fix infrastructure, not a headline.
	for _, l := range f.added {
		if n, ok := declOf(l); ok && !isTestFunc(n) && !isErrSentinel(n) {
			return "add " + splitIdent(n)
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
	"defer": true, "select": true, "make": true, "append": true, "delete": true,
	"len": true, "cap": true, "close": true, "panic": true, "recover": true,
	"struct": true, "interface": true, "chan": true, "map": true, "type": true,
	"case": true, "default": true, "switch": true, "goto": true, "break": true,
	"continue": true, "fallthrough": true, "range": true, "true": true, "false": true,
	"iota": true, "byte": true, "rune": true, "uint": true, "uint8": true,
	"uint16": true, "uint32": true, "uint64": true, "int8": true, "int16": true,
	"int32": true, "int64": true, "float32": true, "float64": true, "any": true,
	"fmt": true, "log": true, "time": true, "sync": true, "context": true,
	"lock": true, "unlock": true, "rlock": true, "runlock": true,
	"git": true, "gitdiff": true, "gitdiffc": true, "exec": true, "command": true, "fatal": true,
	"stat": true, "mode": true, "trim": true, "trimspace": true, "trimprefix": true, "trimsuffix": true,
	"split": true, "join": true, "contains": true, "hasprefix": true, "hassuffix": true,
	"read": true, "write": true, "readall": true, "output": true,
}

func isStopWord(w string) bool {
	if len(w) <= 2 {
		return true
	}
	return stopWords[w]
}

func filepathSlash(p string) string { return strings.ReplaceAll(p, "\\", "/") }
