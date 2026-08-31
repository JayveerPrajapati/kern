package whatif

import (
	"path/filepath"
	"regexp"
	"strings"
)

// ExtractSymbols pulls candidate symbol identifiers from a natural-language
// change description. It is deterministic (no LLM). Candidates are returned in
// priority order: quoted > qualified-name > file-stem > bare-CamelCase.
// Returns nil when the input looks like a bare symbol already (no spaces).
func ExtractSymbols(change string) []string {
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
	quotedRe := regexp.MustCompile("`([A-Za-z_][A-Za-z0-9_.]*)`|\"([A-Za-z_][A-Za-z0-9_.]*)\"")
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
	qualifiedRe := regexp.MustCompile(`[A-Za-z_][A-Za-z0-9_]*(\.[A-Za-z_][A-Za-z0-9_]*)+`)
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
	fileRe := regexp.MustCompile(`[\w./-]+\.go(?::\d+)?`)
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
	bareRe := regexp.MustCompile(`\b[a-zA-Z_][a-zA-Z0-9_]{2,}\b`)
	stop := map[string]bool{}
	for _, w := range []string{
		"the", "a", "an", "and", "or", "but", "not", "for", "with", "from",
		"into", "to", "of", "in", "on", "at", "by", "is", "are", "was",
		"were", "be", "been", "being", "has", "have", "had", "do", "does",
		"did", "will", "would", "should", "could", "may", "might", "can",
		"this", "that", "these", "those", "it", "its", "they", "them",
		"their", "we", "you", "your", "our", "his", "her", "him", "she",
		"who", "which", "what", "when", "where", "why", "how",
		"refactor", "remove", "change", "add", "delete", "update", "split",
		"move", "create", "introduce", "modify", "replace", "rewrite",
		"rename", "extract", "inline", "simplify", "clean", "fix", "break",
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
	} {
		stop[strings.ToLower(w)] = true
	}
	var bare []string
	for _, m := range bareRe.FindAllString(change, -1) {
		if !stop[strings.ToLower(m)] {
			bare = append(bare, m)
		}
	}

	// Qualified names yield the most reliable symbol: prefer the last
	// component as the bare symbol, full qualified name as a candidate.
	ordered := make([]string, 0, len(quoted)+len(qualified)+len(fileStems)+len(bare))
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
