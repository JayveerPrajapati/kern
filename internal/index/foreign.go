package index

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
)

// detectLang maps a file path (and optional first-line shebang) to a language
// id. Returns "" for unsupported files.
func detectLang(rel string, src []byte) string {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go":
		return "go"
	case ".py":
		return "python"
	case ".js", ".mjs", ".cjs", ".jsx":
		return "javascript"
	case ".ts", ".tsx":
		return "typescript"
	case ".vue", ".svelte":
		return sfcLang(src)
	case ".astro":
		return astroLang(src)
	case ".css", ".scss", ".less":
		return "css"
	case ".html", ".htm":
		return "html"
	case ".md", ".mdx", ".markdown":
		return "markdown"
	case ".json", ".jsonc":
		return "json"
	case ".yml", ".yaml":
		return "yaml"
	case ".rs":
		return "rust"
	case ".c", ".h":
		return "c"
	case ".cc", ".cpp", ".cxx", ".hpp", ".hxx":
		return "cpp"
	case ".cs":
		return "csharp"
	case ".java":
		return "java"
	case ".rb":
		return "ruby"
	case ".php":
		return "php"
	case ".sh", ".bash":
		return "shell"
	case ".dart":
		return "dart"
	}
	if strings.HasPrefix(string(src), "#!") {
		first := firstLine(src)
		lower := strings.ToLower(first)
		switch {
		case strings.Contains(lower, "python"):
			return "python"
		case strings.Contains(lower, "bash"), strings.Contains(lower, "sh"), strings.Contains(lower, "zsh"):
			return "shell"
		case strings.Contains(lower, "node"):
			return "javascript"
		case strings.Contains(lower, "ruby"):
			return "ruby"
		}
	}
	return ""
}

func firstLine(src []byte) string {
	if i := strings.IndexByte(string(src), '\n'); i >= 0 {
		return string(src[:i])
	}
	return string(src)
}

// sfcLang reports the script language of a single-file component (.vue/.svelte)
// from the first <script> tag's attributes: lang="ts" -> typescript, else
// javascript.
func sfcLang(src []byte) string {
	si := bytes.Index(src, []byte("<script"))
	if si < 0 {
		return "javascript"
	}
	rest := src[si:]
	end := bytes.IndexByte(rest, '>')
	if end < 0 {
		return "javascript"
	}
	lower := strings.ToLower(string(rest[:end]))
	if strings.Contains(lower, `lang="ts"`) || strings.Contains(lower, `lang="tsx"`) {
		return "typescript"
	}
	return "javascript"
}

// astroLang reports the script language of an .astro file. Astro is TS-first:
// the --- frontmatter block is always TypeScript, and TS is a superset of JS,
// so the extractor always runs in TypeScript mode.
func astroLang(src []byte) string {
	return "typescript"
}

// sfcScript returns only the code bodies of a single-file component, so markup
// (template, style, frontmatter) never confuses the JS/TS extractor. Vue/Svelte
// keep only <script> blocks; Astro keeps the --- frontmatter plus <script>s.
// Files that are not SFCs pass through unchanged.
func sfcScript(rel string, src []byte) []byte {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".vue", ".svelte":
		return scriptBlocks(src)
	case ".astro":
		var out []byte
		if fm := astroFrontmatter(src); len(fm) > 0 {
			out = append(out, fm...)
			out = append(out, '\n')
		}
		out = append(out, scriptBlocks(src)...)
		return out
	}
	return src
}

// scriptBlocks returns the concatenated bodies of every <script>...</script>
// block in src.
func scriptBlocks(src []byte) []byte {
	var out []byte
	rest := src
	for {
		start := bytes.Index(rest, []byte("<script"))
		if start < 0 {
			break
		}
		openEnd := bytes.Index(rest[start:], []byte(">"))
		if openEnd < 0 {
			break
		}
		openEnd += start + 1
		close := bytes.Index(rest[openEnd:], []byte("</script>"))
		if close < 0 {
			break
		}
		close += openEnd
		out = append(out, rest[openEnd:close]...)
		out = append(out, '\n')
		rest = rest[close+len("</script>"):]
	}
	return out
}

// astroFrontmatter returns the body of an Astro --- frontmatter block, or nil
// when the file has none at position 0.
func astroFrontmatter(src []byte) []byte {
	if !bytes.HasPrefix(src, []byte("---")) {
		return nil
	}
	rest := src[3:]
	nl := bytes.IndexByte(rest, '\n')
	if nl < 0 {
		return nil
	}
	rest = rest[nl+1:]
	idx := bytes.Index(rest, []byte("\n---"))
	if idx < 0 {
		return nil
	}
	return rest[:idx]
}

// quickExt is a cheap pre-filter before reading a file.
func quickExt(rel string) bool {
	switch strings.ToLower(filepath.Ext(rel)) {
	case ".go", ".py", ".js", ".mjs", ".cjs", ".jsx", ".ts", ".tsx",
		".vue", ".svelte", ".astro", ".css", ".scss", ".less", ".html", ".htm",
		".md", ".mdx", ".markdown", ".json", ".jsonc", ".yml", ".yaml",
		".rs", ".c", ".h", ".cc", ".cpp", ".cxx", ".hpp", ".hxx",
		".cs", ".java", ".rb", ".php", ".sh", ".bash", ".dart":
		return true
	}
	return false
}

// QuickExt reports whether a root-relative file path has a source extension
// the index considers indexable. Used by downstream scanners to reuse the
// index's file-selection policy.
func QuickExt(rel string) bool { return quickExt(rel) }

// isIndexable reports whether a file should be part of the index.
func isIndexable(rel string, src []byte) bool {
	// Binary-content sniffing: a NUL byte in the first 8KB is the standard
	// heuristic (used by git, grep, etc.) that a file is binary. Even a file
	// with a source extension (e.g. data.js) could be binary and would yield
	// garbage symbols if regex-scanned, so check before anything else.
	n := len(src)
	if n > 8192 {
		n = 8192
	}
	if bytes.IndexByte(src[:n], 0x00) >= 0 {
		return false
	}
	// Minified/bundled files are skipped entirely: their truncated variable
	// names and single-line bodies produce garbage symbols and corrupt the
	// call graph, unlike generated code (which still reuses real names).
	if isMinified(src) {
		return false
	}
	return detectLang(rel, src) != ""
}

type declRule struct {
	kind  string
	isDef bool
	recv  int // group index of an explicit receiver (e.g. C++ Type::method); 0 = none
	re    *regexp.Regexp
}

type langSpec struct {
	indent      bool
	lineComment []string
	block       string // "/*" style, empty if none
	blockEnd    string // closes block, default "*/"
	backtick    bool
	triple      bool
	heredoc     bool // Ruby-style <<~HEREDOC heredocs
	rules       []declRule
	entries     []entryRule
	kw          map[string]bool
}

var (
	identRe = `[A-Za-z_$][A-Za-z0-9_$]*`
	// heredocStartRe matches a Ruby heredoc opener like <<~HEREDOC, <<-TERM,
	// or <<TERM, capturing the terminator identifier (group 1).
	heredocStartRe = regexp.MustCompile(`<<(-?~?)([A-Za-z_]\w*)`)
	// callRe matches callee chains like foo(, obj.method(, a.b.c(.
	callRe = regexp.MustCompile(`\b([A-Za-z_$][A-Za-z0-9_$]*)(?:\.[A-Za-z_$][A-Za-z0-9_$]*)*\s*\(`)
)

// typeKinds are declaration kinds that define a type (never a call site).
var typeKinds = map[string]bool{
	"class": true, "interface": true, "enum": true, "trait": true,
	"module": true, "impl": true, "union": true, "struct": true,
}

func kwSet(words ...string) map[string]bool {
	m := make(map[string]bool, len(words))
	for _, w := range words {
		m[w] = true
	}
	return m
}

// init builds the language registry. Per-family rule slices and keyword sets
// (js, python, rust, cfam, java, csharp, ruby, php, shell, css, html, markdown,
// json, yaml) live in foreign_family.go as package-level vars; this init keeps
// the specs map literal as the single writer of the specs global.
func init() {
	specs = map[string]*langSpec{
		"javascript": {lineComment: []string{"//"}, block: "/*", backtick: true, rules: js, entries: entryRules["javascript"], kw: jsKw},
		"typescript": {lineComment: []string{"//"}, block: "/*", backtick: true, rules: js, entries: entryRules["typescript"], kw: jsKw},
		"python":     {indent: true, lineComment: []string{"#"}, triple: true, rules: python, entries: entryRules["python"], kw: pyKw},
		"rust":       {lineComment: []string{"//"}, block: "/*", rules: rust, kw: rsKw},
		"c":          {lineComment: []string{"//"}, block: "/*", rules: cfam, kw: cfamKw},
		"cpp":        {lineComment: []string{"//"}, block: "/*", rules: cfam, kw: cfamKw},
		"java":       {lineComment: []string{"//"}, block: "/*", rules: java, entries: entryRules["java"], kw: javaKw},
		"csharp":     {lineComment: []string{"//"}, block: "/*", rules: csharp, kw: csKw},
		"ruby":       {indent: true, lineComment: []string{"#"}, heredoc: true, rules: ruby, entries: entryRules["ruby"], kw: rubyKw},
		"php":        {lineComment: []string{"//", "#"}, block: "/*", rules: php, entries: entryRules["php"], kw: phpKw},
		"shell":      {lineComment: []string{"#"}, rules: shell, kw: shKw},
		"css":        {lineComment: nil, block: "/*", rules: css, kw: cssKw},
		"html":       {lineComment: nil, block: "<!--", blockEnd: "-->", rules: htmlRules, kw: htmlKw},
		"markdown":   {lineComment: nil, block: "<!--", blockEnd: "-->", rules: markdown, kw: mdKw},
		"json":       {lineComment: []string{"//"}, block: "/*", rules: jsonRules, kw: jsonKw},
		"yaml":       {lineComment: []string{"#"}, rules: yaml, kw: yamlKw},
	}
}

var specs map[string]*langSpec

type stripState struct {
	inBlock   bool
	inTriple  string // active triple-quote delimiter ("" = not inside one)
	inHeredoc string // Ruby heredoc terminator ("" = not in one)
}

type ffile struct {
	lines  []string
	clean  []string
	indent []int
	preD   []int
	postD  []int
	blank  []bool
	com    []bool
}

func analyze(src []byte, spec *langSpec) *ffile {
	raw := strings.Split(string(src), "\n")
	n := len(raw)
	f := &ffile{
		lines: raw, clean: make([]string, n),
		indent: make([]int, n), preD: make([]int, n), postD: make([]int, n),
		blank: make([]bool, n), com: make([]bool, n),
	}
	depth := 0
	st := &stripState{}
	for i, ln := range raw {
		clean := stripLine(ln, spec, st)
		f.clean[i] = clean
		trimmed := strings.TrimSpace(ln)
		f.indent[i] = leadingSpaces(ln)
		f.blank[i] = trimmed == ""
		f.com[i] = isCommentLine(trimmed, spec)
		f.preD[i] = depth
		opens, closes := braceDelta(clean)
		depth += opens - closes
		if depth < 0 {
			depth = 0
		}
		f.postD[i] = depth
	}
	return f
}

func isCommentLine(trimmed string, spec *langSpec) bool {
	for _, c := range spec.lineComment {
		if strings.HasPrefix(trimmed, c) {
			return true
		}
	}
	if spec.block != "" && strings.HasPrefix(trimmed, spec.block) {
		return true
	}
	return false
}
