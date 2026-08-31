package code

// Body folding and tier selection. A file can be rendered at one of three
// tiers:
//
//   - TierFull:   the complete original source, unchanged (default).
//   - TierFolded: function/method bodies are replaced with a placeholder that
//     records how many lines were elided; signatures, types, consts and the
//     package/import headers are preserved verbatim so the structure stays
//     readable and the full file is one request away.
//   - TierSummary: the symbolic summary produced by Summarize (path + symbol
//     list with line numbers).
//
// Go files are folded with go/ast (exact brace positions, no regex guessing).
// Other known languages fall back to a line-based fold (brace matching for
// "{}"-style languages, indentation for python/ruby/shell). Files of unknown
// language are returned unchanged rather than risk mangling.

import (
	"fmt"
	"go/ast"
	"go/parser"
	"go/token"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// Tier selects how much of a file's body is included when rendering.
type Tier int

const (
	// TierFull is the default: the complete original source, unchanged.
	TierFull Tier = iota
	// TierFolded replaces function/method bodies with an elided-lines
	// placeholder, keeping signatures, types and consts.
	TierFolded
	// TierSummary is the symbolic summary (path + symbol list).
	TierSummary
)

// ParseTier parses a flag value into a Tier. The empty string maps to TierFull
// (the library default). Callers that want to preserve a tool's historical
// summary behavior must default to TierSummary themselves.
func ParseTier(s string) (Tier, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "full":
		return TierFull, nil
	case "folded", "fold":
		return TierFolded, nil
	case "summary", "sum":
		return TierSummary, nil
	}
	return TierFull, fmt.Errorf("unknown tier %q (want full, folded or summary)", s)
}

func (t Tier) String() string {
	switch t {
	case TierFolded:
		return "folded"
	case TierSummary:
		return "summary"
	default:
		return "full"
	}
}

// RenderTier renders a file at the requested tier. TierFull passes the
// original source through unchanged, TierFolded folds bodies, TierSummary
// returns the symbolic summary. This is the single entry point used by the CLI
// and MCP handlers for the --tier / tier argument.
func RenderTier(path string, content []byte, tier Tier) string {
	switch tier {
	case TierFolded:
		return string(Fold(path, content))
	case TierSummary:
		return Summarize(path, content, 200).Render()
	default:
		return string(content)
	}
}

// Fold returns the tier=folded rendering of a file: function/method bodies are
// replaced with a "… body elided: N lines …" placeholder (comment syntax
// matched to the language), while signatures, types, consts and the
// package/import headers are preserved. The placeholder always records the
// number of elided lines so an agent knows exactly what was dropped and can
// request the full file.
func Fold(path string, content []byte) []byte {
	lang := DetectLanguage(path)
	switch lang {
	case "go":
		if folded, ok := foldGo(content); ok {
			return folded
		}
		// Unparseable Go: fall through to the generic line fold.
	}
	if lang != "" {
		if folded, ok := foldLines(lang, content); ok {
			return folded
		}
	}
	return content
}

// elidedLines returns the placeholder comment for n elided lines using the
// language's comment marker, e.g. "// ... body elided: 3 lines ...".
func elidedLines(comment, indent string, n int) string {
	return indent + comment + " ... body elided: " + strconv.Itoa(n) + " lines ..."
}

// foldGo folds Go function bodies using go/ast. It rebuilds the source by
// replacing the interior lines of every multi-line function/method body (the
// lines strictly between '{' and '}') with an elided-lines placeholder; every
// other line — signatures, types, consts, comments — is kept byte-for-byte.
// It returns (nil, false) when the file cannot be parsed, so the caller can
// fall back to a line-based fold.
func foldGo(content []byte) ([]byte, bool) {
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "fold.go", content, parser.ParseComments)
	if err != nil {
		return nil, false
	}
	tf := fset.File(f.Pos())
	if tf == nil {
		return content, true // empty file: nothing to fold
	}
	type span struct{ start, end int } // 0-based line indices, inclusive
	var spans []span
	ast.Inspect(f, func(n ast.Node) bool {
		fd, ok := n.(*ast.FuncDecl)
		if !ok || fd.Body == nil {
			return true
		}
		// Lbrace/Rbrace are 1-based lines. The interior (elided) lines are
		// Lbrace+1 .. Rbrace-1 (1-based), i.e. 0-based indices Lbrace .. Rbrace-2.
		lb := fset.Position(fd.Body.Lbrace).Line
		rb := fset.Position(fd.Body.Rbrace).Line
		if rb-lb >= 2 {
			spans = append(spans, span{lb, rb - 2})
		}
		return true
	})
	if len(spans) == 0 {
		return content, true
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	lines := strings.Split(string(content), "\n")
	var out []string
	last := 0
	for _, sp := range spans {
		if sp.start > sp.end || sp.start < last {
			continue // defensive: should not happen for FuncDecl bodies
		}
		out = append(out, lines[last:sp.start]...)
		indent := "\t"
		if sp.start < len(lines) {
			if w := leadingWS(lines[sp.start]); w != "" {
				indent = w
			}
		}
		out = append(out, elidedLines("//", indent, sp.end-sp.start+1))
		last = sp.end + 1
	}
	out = append(out, lines[last:]...)
	return []byte(strings.Join(out, "\n")), true
}

// funcDeclRe matches the function-like declaration lines that are foldable per
// language. Class/type declarations are deliberately excluded: the tier system
// keeps types intact, only function/method bodies are elided.
var funcDeclRe = map[string]*regexp.Regexp{
	"js":     regexp.MustCompile(`(?:^|\s)(?:export\s+)?(?:async\s+)?function\s+`),
	"java":   regexp.MustCompile(`\b[A-Za-z_]\w*\s*\([^;]*\)\s*(?:throws[^{]*)?\{`),
	"c":      regexp.MustCompile(`^[A-Za-z_][\w\s\*\[\]]+\([^;]*\)\s*\{`),
	"rust":   regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+`),
	"python": regexp.MustCompile(`^\s*(?:async\s+)?def\s+`),
	"ruby":   regexp.MustCompile(`^\s*def\s+`),
	"shell":  regexp.MustCompile(`^\s*(?:function\s+)?[A-Za-z_]\w*\s*\(\)`),
}

// braceFolded reports whether lang folds blocks by matching '{' ... '}'.
func braceFolded(lang string) bool {
	switch lang {
	case "js", "java", "c", "rust", "shell":
		return true
	}
	return false
}

// commentMarker returns the line-comment prefix used for placeholders in lang.
func commentMarker(lang string) string {
	switch lang {
	case "python", "ruby", "shell":
		return "#"
	default:
		return "//"
	}
}

// foldLines folds function-like bodies for non-Go languages using line-based
// heuristics:
//   - brace languages (js, java, c, rust, shell): a function declaration whose
//     block opens with '{' is folded to its matching '}' by counting braces,
//     ignoring braces inside quoted strings.
//   - indentation languages (python, ruby): the body is every non-blank line
//     until the next line whose indentation is <= the declaration's.
//
// The heuristic is honest about its limits: braces inside comments or complex
// template expressions can mislead the counter, in which case the fold simply
// covers a wrong-but-valid range and the placeholders still report the line
// count. Returns (nil, false) when nothing foldable was found.
func foldLines(lang string, content []byte) ([]byte, bool) {
	re := funcDeclRe[lang]
	if re == nil {
		return nil, false
	}
	comment := commentMarker(lang)
	lines := strings.Split(string(content), "\n")
	type span struct{ start, end int } // 0-based line indices, inclusive
	var spans []span
	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || !re.MatchString(line) {
			continue
		}
		if braceFolded(lang) {
			open := i
			if !strings.Contains(trimmed, "{") {
				// Signature may be followed by a lone '{' on the next line
				// (e.g. rust/shell). One-line blocks are left alone.
				if open+1 < len(lines) && strings.TrimSpace(lines[open+1]) == "{" {
					open = i + 1
				} else {
					continue
				}
			}
			end, ok := matchBrace(lines, open)
			if !ok || end <= open+1 {
				continue
			}
			spans = append(spans, span{open + 1, end - 1})
			continue
		}
		// Indentation fold (python/ruby). Skip one-liners where the body sits
		// on the declaration line (e.g. "def f(): return 1" or "def f; x; end").
		if strings.Contains(trimmed, ";") {
			continue
		}
		indent := leadingWS(line)
		body := i + 1
		lastBody := -1
		for end := body; end < len(lines); end++ {
			l := lines[end]
			if strings.TrimSpace(l) == "" {
				continue // blank lines stay outside the elided span
			}
			if len(leadingWS(l)) <= len(indent) {
				break
			}
			lastBody = end
		}
		if lastBody < body {
			continue
		}
		spans = append(spans, span{body, lastBody})
	}
	if len(spans) == 0 {
		return nil, false
	}
	sort.Slice(spans, func(i, j int) bool { return spans[i].start < spans[j].start })
	var out []string
	last := 0
	for _, sp := range spans {
		if sp.start > sp.end || sp.start < last {
			continue
		}
		out = append(out, lines[last:sp.start]...)
		indent := "\t"
		if sp.start < len(lines) {
			if w := leadingWS(lines[sp.start]); w != "" {
				indent = w
			}
		}
		out = append(out, elidedLines(comment, indent, sp.end-sp.start+1))
		last = sp.end + 1
	}
	out = append(out, lines[last:]...)
	return []byte(strings.Join(out, "\n")), true
}

// matchBrace finds the 0-based index of the '}' that closes the block opened
// on line open, counting braces while ignoring braces inside quoted strings
// (single, double and backtick, honoring backslash escapes).
func matchBrace(lines []string, open int) (int, bool) {
	depth := 0
	for i := open; i < len(lines); i++ {
		inStr := rune(0)
		esc := false
		for _, r := range lines[i] {
			if inStr != 0 {
				if esc {
					esc = false
					continue
				}
				if r == '\\' {
					esc = true
					continue
				}
				if r == inStr {
					inStr = 0
				}
				continue
			}
			switch r {
			case '\'', '"', '`':
				inStr = r
			case '{':
				depth++
			case '}':
				depth--
				if depth == 0 {
					return i, true
				}
			}
		}
	}
	return 0, false
}

// leadingWS returns the leading whitespace (spaces/tabs) of s.
func leadingWS(s string) string {
	i := 0
	for i < len(s) && (s[i] == ' ' || s[i] == '\t') {
		i++
	}
	return s[:i]
}
