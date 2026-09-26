package whatif

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// Symbol-extraction regexes, compiled once at package init instead of per
// ExtractSymbols call.
var (
	// quotedRe matches backtick / double-quoted symbol references.
	quotedRe = regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_.]*)`|\"([A-Za-z_][A-Za-z0-9_.]*)\"")
	// qualifiedRe matches dotted qualified names: pkg.Symbol / Type.Method.
	qualifiedRe = regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)+`)
	// fileRe matches file-path references with optional :line suffix.
	fileRe = regexp.MustCompile(`[\w./-]+\.go(?::\d+)?`)
	// bareRe matches word-bounded bare identifiers of 3+ chars.
	bareRe = regexp.MustCompile(`\b[a-zA-Z_][a-zA-Z0-9_]{2,}\b`)
)

// ExtractSymbols pulls candidate symbol identifiers from a natural-language
// change description. It is deterministic (no LLM). Candidates are returned in
// priority order: quoted > qualified-name > file-stem > bare-CamelCase.
// Returns nil when the input looks like a bare symbol already (no spaces).
func ExtractSymbols(change string) []string {
	return extractSymbols(change, nil)
}

// ExtractSymbolsIndex is ExtractSymbols with an index consult: a bare token
// that resolves (case-sensitively) to a real index symbol is kept even when it
// collides with a common English/change verb (Add, Get, Run, Set, Make, Call,
// Fix, ...). Stopword filtering still applies to every token that does NOT
// resolve to an index symbol. A nil index behaves exactly like ExtractSymbols.
func ExtractSymbolsIndex(change string, ix *index.Index) []string {
	return extractSymbols(change, ix)
}

// changeVerbs are the verbs that headline change descriptions ("remove X",
// "rename X to Y"). They are stoplisted during symbol extraction AND used by
// the CLI doors to detect unquoted multi-word changes: `kern impact remove
// WriteFileAtomic` arrives as args[0]="remove", args[1]="WriteFileAtomic"
// (the second parsed as the [kind] positional), and the verb-first pattern
// means the user forgot to quote the sentence — the positionals are joined
// instead of fuzzy-resolving the bare verb to an unrelated symbol (QA
// campaign: impact/analyze/what-if/plan all resolved 'remove' to
// Client.Remove and confidently reported on it).
var changeVerbs = map[string]bool{
	"refactor": true, "remove": true, "change": true, "add": true,
	"delete": true, "update": true, "split": true, "move": true,
	"create": true, "introduce": true, "modify": true, "replace": true,
	"rewrite": true, "rename": true, "extract": true, "inline": true,
	"simplify": true, "clean": true, "fix": true, "break": true,
	// Inflected change-verbs: 3rd-person / past forms of the verbs above
	// headline prose ("what breaks if I remove X") and must never outrank
	// a real symbol that follows them.
	"refactors": true, "removes": true, "removed": true, "changes": true,
	"changed": true, "adds": true, "added": true, "deletes": true,
	"deleted": true, "updates": true, "updated": true, "renames": true,
	"renamed": true, "moves": true, "moved": true, "extracts": true,
	"simplifies": true, "splits": true, "breaks": true, "fixes": true,
	"fixed": true, "modifies": true, "replaces": true, "replaced": true,
	"rewrites": true, "introduces": true, "creates": true,
}

// IsChangeVerb reports whether w is a change verb from the extraction
// stoplist (remove, add, rename, ...) — the words that headline an
// unquoted multi-word change on the CLI.
func IsChangeVerb(w string) bool {
	return changeVerbs[strings.ToLower(w)]
}

func extractSymbols(change string, ix *index.Index) []string {
	change = strings.TrimSpace(change)
	if change == "" {
		return nil
	}
	// If the input has no spaces it is already a bare symbol — pass it through
	// untouched rather than over-processing it.
	if !strings.ContainsAny(change, " \t") {
		return []string{change}
	}

	// 1. Backtick / double-quote quoted symbols.
	var quoted []string
	for _, m := range quotedRe.FindAllStringSubmatch(change, -1) {
		if m[1] != "" {
			quoted = append(quoted, m[1])
		} else if m[2] != "" {
			quoted = append(quoted, m[2])
		}
	}

	// 2. Qualified names: pkg.Symbol / Type.Method / pkg.Type.Method. A match
	// whose last segment is a known file extension (e.g. `db_connections.go`) is
	// really a file path, not a qualified name — leave it for file-stem handling.
	ext := map[string]bool{
		".go": true, ".py": true, ".js": true, ".ts": true, ".tsx": true,
		".jsx": true, ".rs": true, ".java": true, ".c": true, ".cpp": true,
		".h": true, ".md": true, ".json": true, ".yaml": true, ".yml": true,
		".sh": true, ".rb": true, ".sql": true,
	}
	var qualified []string
	for _, m := range qualifiedRe.FindAllString(change, -1) {
		last := m
		if i := strings.LastIndexByte(m, '.'); i >= 0 {
			last = m[i:]
		}
		if !ext[last] {
			qualified = append(qualified, m)
		}
	}

	// 3. File-path references with optional line: path/to/file.go:42.
	var fileStems []string
	for _, m := range fileRe.FindAllString(change, -1) {
		stem := strings.TrimSuffix(m, filepath.Ext(m))
		stem = strings.TrimSuffix(stem, ".go")
		// Trim any :line suffix captured into the stem.
		if i := strings.IndexByte(stem, ':'); i >= 0 {
			stem = stem[:i]
		}
		stem = filepath.Base(stem)
		// Only keep stems that could plausibly be symbols (contain a letter).
		if hasLetter(stem) {
			fileStems = append(fileStems, stem)
		}
	}

	// 4. Bare identifiers (CamelCase, camelCase, snake_case, ALL_CAPS), filtered
	// against a case-insensitive common-word stoplist. The regex matches any
	// word-bounded identifier of 3+ chars starting with a letter or underscore,
	// so it captures loadQuestion, replicaCount, process_service_request and
	// GetMySQLDB alike — not just uppercase-leading CamelCase.
	stop := map[string]bool{}
	for w := range changeVerbs {
		stop[w] = true
	}
	for _, w := range []string{
		"the", "a", "an", "and", "or", "but", "not", "for", "with", "from",
		"into", "to", "of", "in", "on", "at", "by", "is", "are", "was",
		"were", "be", "been", "being", "has", "have", "had", "do", "does",
		"did", "will", "would", "should", "could", "may", "might", "can",
		"this", "that", "these", "those", "it", "its", "they", "them",
		"their", "we", "you", "your", "our", "his", "her", "him", "she",
		"who", "which", "what", "when", "where", "why", "how",
		"method", "function", "file", "symbol", "code", "line", "lines",
		"class", "struct", "type", "interface", "module", "package",
		"variable", "constant", "field", "property", "parameter", "argument",
		"return", "returns", "value", "values", "name", "names",
		"true", "false", "null", "none", "nil", "void",
		"new", "old", "all", "some", "any", "each", "every",
		"first", "last", "next", "prev", "previous",
		"use", "using", "used", "uses", "via", "through",
		"about", "above", "below", "over", "under", "between",
		"more", "less", "most", "least", "very", "much",
		"than", "then", "so", "if", "else", "end", "begin", "start",
		"test", "tests", "unit", "integration", "bug", "issue", "error",
		"feature", "task", "todo", "note", "notes",
		"get", "set", "put", "post", "make", "run", "try", "call",
		"one", "two", "three", "four", "five",
		// Filesystem / path words that appear as bare words around file paths
		// but are not symbols.
		"just", "prose", "here", "there", "symbols", "connections",
		"directory", "folder", "path", "root", "source", "config",
		"service", "handler", "client", "server", "response", "request",
		"endpoint", "endpoints", "api", "apis", "rest", "lag",
	} {
		stop[strings.ToLower(w)] = true
	}
	var bare, verbBare []string
	for _, m := range bareRe.FindAllString(change, -1) {
		// A token that resolves (case-sensitively) to a real index symbol
		// is kept even when it collides with the stoplist (e.g. "Add",
		// "Fix"); the stoplist applies only to tokens that do NOT resolve.
		if stop[strings.ToLower(m)] && !hasIndexSymbol(ix, m) {
			continue
		}
		// ...but a token that is BOTH a real symbol AND sentence-verb-shaped
		// ("add a caching layer to Fit" — a lowercase `add` helper exists
		// somewhere in most codebases) is DEMOTED to the end of the
		// candidate order instead of outranking the symbols it headlines.
		// Backticks (`add`) still quote-win and take precedence.
		if changeVerbs[strings.ToLower(m)] {
			verbBare = append(verbBare, m)
			continue
		}
		bare = append(bare, m)
	}
	bare = append(bare, verbBare...)

	// Qualified names yield the most reliable symbol: prefer the last
	// component as the bare symbol, full qualified name as a candidate.
	capHint := int64(len(quoted)) + int64(len(qualified)) + int64(len(fileStems)) + int64(len(bare))
	if capHint < 0 || capHint > 1_000_000 {
		capHint = 0
	}
	ordered := make([]string, 0, int(capHint))
	seen := make(map[string]bool)
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		ordered = append(ordered, s)
	}

	for _, q := range quoted {
		add(q)
	}
	for _, qn := range qualified {
		add(qn)
	}
	// Bare CamelCase identifiers before file basenames: a symbol like
	// `GetMySQLDB` is the primary target and must outrank a file basename
	// such as `db_connections` that merely anchors the location in prose.
	covered := make(map[string]bool)
	for _, q := range quoted {
		covered[q] = true
	}
	for _, qn := range qualified {
		covered[qn] = true
		for _, part := range strings.Split(qn, ".") {
			covered[part] = true
		}
	}
	for _, b := range bare {
		if !covered[b] {
			add(b)
		}
	}
	for _, fs := range fileStems {
		add(fs)
	}

	if len(ordered) > 5 {
		ordered = ordered[:5]
	}
	return ordered
}

func hasLetter(s string) bool {
	for _, r := range s {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') {
			return true
		}
	}
	return false
}

// hasIndexSymbol reports whether the index contains a symbol whose simple name
// (or full name) equals name case-sensitively. A nil index never matches, so
// ExtractSymbols keeps its pure stopword behavior when no index is consulted.
func hasIndexSymbol(ix *index.Index, name string) bool {
	if ix == nil {
		return false
	}
	for _, s := range ix.Symbols {
		if s.Name == name || s.FullName() == name {
			return true
		}
	}
	return false
}

// IsNetNewFeature reports whether a natural-language change intent appears to
// describe a net-new capability (e.g. "Add ...", "Create ...", "Introduce ...")
// rather than an alteration to an existing symbol.
func IsNetNewFeature(intent string) bool {
	lower := strings.ToLower(strings.TrimSpace(intent))
	prefixes := []string{
		"add ", "create ", "introduce ", "implement ", "new ", "build ",
	}
	for _, p := range prefixes {
		if strings.HasPrefix(lower, p) {
			return true
		}
	}
	return false
}
