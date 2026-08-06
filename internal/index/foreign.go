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
		".cs", ".java", ".rb", ".php", ".sh", ".bash":
		return true
	}
	return false
}

// isIndexable reports whether a file should be part of the index.
func isIndexable(rel string, src []byte) bool {
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
	rules       []declRule
	kw          map[string]bool
}

var (
	identRe = `[A-Za-z_$][A-Za-z0-9_$]*`
	// stripStringRe removes "..."/'...' literals (with backslash escapes).
	stripStringRe = regexp.MustCompile(`"(?:\\.|[^"\\])*"|'(?:\\.|[^'\\])*'`)
	backtickRe    = regexp.MustCompile("`(?:\\\\.|[^`\\\\])*`")
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

func init() {
	js := []declRule{
		{kind: "class", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+(?:default\s+)?)?class\s+(` + identRe + `)`)},
		{kind: "interface", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?interface\s+(` + identRe + `)`)},
		{kind: "enum", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?enum\s+(` + identRe + `)`)},
		{kind: "type", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?type\s+(` + identRe + `)\s*=`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?(?:const|let|var)\s+(` + identRe + `)\s*=\s*(?:async\s+)?\(`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?(?:const|let|var)\s+(` + identRe + `)\s*=\s*(?:async\s+)?` + identRe + `\s*=>`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?(?:const|let|var)\s+(` + identRe + `)\s*=\s*(?:async\s+)?function`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+(?:default\s+)?)?(?:async\s+)?function\s*\*?\s*(` + identRe + `)\s*(?:<[^>]*>)?\s*\(`)},
		{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:(?:get|set|static|async)\s+)*(?:#)?(` + identRe + `)\s*(?:<[^>]*>)?\s*\([^)]*\)\s*(?::[^{}]*)?\{`)},
		{kind: "const", re: regexp.MustCompile(`(?:^|[\s;])(?:export\s+)?(?:const|let|var)\s+(` + identRe + `)\s*=`)},
	}
	jsKw := kwSet("if", "for", "while", "switch", "catch", "return", "do", "case", "function",
		"class", "new", "delete", "typeof", "instanceof", "throw", "try", "with", "else",
		"in", "of", "void", "yield", "await", "super", "this", "extends", "implements",
		"interface", "type", "enum", "import", "export", "from", "as", "async", "static",
		"get", "set", "const", "let", "var", "finally", "default", "null", "undefined",
		"true", "false")

	python := []declRule{
		{kind: "class", re: regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:async\s+)?def\s+([A-Za-z_]\w*)\s*\(`)},
	}
	pyKw := kwSet("if", "elif", "else", "for", "while", "with", "and", "or", "not", "in",
		"is", "lambda", "def", "class", "return", "import", "from", "assert", "del",
		"raise", "except", "finally", "pass", "break", "continue", "as", "global",
		"nonlocal", "yield", "try", "match", "case", "None", "True", "False")

	rust := []declRule{
		{kind: "struct", re: regexp.MustCompile(`^\s*(?:pub\s+)?struct\s+([A-Za-z_]\w*)`)},
		{kind: "enum", re: regexp.MustCompile(`^\s*(?:pub\s+)?enum\s+([A-Za-z_]\w*)`)},
		{kind: "trait", re: regexp.MustCompile(`^\s*(?:pub\s+)?trait\s+([A-Za-z_]\w*)`)},
		{kind: "impl", re: regexp.MustCompile(`^\s*(?:pub\s+)?impl\s+(?:<[^>]*>\s*)?([A-Za-z_]\w*)`)},
		{kind: "type", re: regexp.MustCompile(`^\s*(?:pub\s+)?type\s+([A-Za-z_]\w*)\s*=`)},
		{kind: "const", re: regexp.MustCompile(`^\s*(?:pub\s+)?const\s+([A-Za-z_]\w*)\s*:`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:pub\s+)?(?:async\s+)?fn\s+([A-Za-z_]\w*)\s*(?:<[^>]*>\s*)?\(`)},
	}
	rsKw := kwSet("if", "else", "for", "while", "match", "loop", "fn", "return", "let",
		"mut", "const", "static", "struct", "enum", "trait", "impl", "use", "mod", "pub",
		"move", "as", "in", "where", "async", "await", "dyn", "ref", "unsafe", "break",
		"continue", "true", "false", "None", "Some", "Ok", "Err")

	cfam := []declRule{
		{kind: "struct", re: regexp.MustCompile(`^\s*(?:typedef\s+)?struct\s+([A-Za-z_]\w*)`)},
		{kind: "enum", re: regexp.MustCompile(`^\s*(?:typedef\s+)?enum\s+([A-Za-z_]\w*)`)},
		{kind: "union", re: regexp.MustCompile(`^\s*(?:typedef\s+)?union\s+([A-Za-z_]\w*)`)},
		{kind: "class", re: regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*)`)},
		{kind: "method", isDef: true, recv: 1, re: regexp.MustCompile(`^\s*(?:(?:inline|static|virtual|constexpr)\s+)*(?:[A-Za-z_][\w<>&*\s]*\s+)?([A-Za-z_]\w*)::([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:const\s*)?\{`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:(?:inline\s+|static\s+|virtual\s+|constexpr\s+|extern\s+"[^"]*"\s+)*[A-Za-z_][\w:<>*&\s]*\s+)?([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:const\s*)?(?:override\s*)?(?:noexcept\s*)?\{`)},
	}
	cfamKw := kwSet("if", "else", "for", "while", "switch", "return", "sizeof", "do",
		"case", "try", "catch", "throw", "new", "delete", "typeof", "const", "static",
		"void", "struct", "union", "enum", "class", "namespace", "using", "template",
		"typename", "continue", "break", "goto", "true", "false", "NULL", "nullptr",
		"int", "char", "float", "double", "long", "short", "unsigned", "signed",
		"bool", "auto", "register", "extern", "inline", "virtual", "override",
		"public", "private", "protected", "this")

	java := []declRule{
		{kind: "class", re: regexp.MustCompile(`^\s*(?:public|private|protected|abstract|final|sealed)?\s*(?:class|interface|enum|record)\s+([A-Za-z_]\w*)`)},
		{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract|synchronized|native|default|strictfp)\s+)*[A-Za-z_][\w<>\[\].,?\s]*?\s+([A-Za-z_]\w*)\s*\([^;{}]*\)\s*(?:throws\s+[\w.,\s]+)?\{`)},
		{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:public|private|protected)?\s*([A-Za-z_]\w*)\s*\([^;{}]*\)\s*\{`)},
	}
	javaKw := kwSet("if", "else", "for", "while", "switch", "return", "new", "throw",
		"try", "catch", "finally", "synchronized", "instanceof", "case", "do",
		"continue", "break", "assert", "void", "this", "super", "class", "interface",
		"enum", "extends", "implements", "package", "import", "static", "final",
		"abstract", "public", "private", "protected", "null", "true", "false",
		"int", "long", "short", "byte", "char", "float", "double", "boolean")

	csharp := []declRule{
		{kind: "class", re: regexp.MustCompile(`^\s*(?:public|internal|sealed|abstract|partial|static|readonly|file)?\s*(?:class|interface|record|struct|enum)\s+([A-Za-z_]\w*)`)},
		{kind: "prop", isDef: true, re: regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|readonly|virtual|override|new|async)?\s*[A-Za-z_][\w<>?\[\],.\s]*\s+([A-Za-z_]\w*)\s*\{\s*(?:get|set|init)`)},
		{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:public|private|protected|internal|static|virtual|override|async|new|extern|partial|sealed)?\s*[A-Za-z_][\w<>?\[\],.\s]*\s+([A-Za-z_]\w*)\s*\([^;{}]*\)\s*\{`)},
		{kind: "method", isDef: true, re: regexp.MustCompile(`^\s*(?:public|private|protected|internal)?\s*([A-Za-z_]\w*)\s*\([^;{}]*\)\s*\{`)},
	}
	csKw := kwSet("if", "else", "for", "foreach", "while", "switch", "return", "using",
		"namespace", "new", "typeof", "throw", "try", "catch", "finally", "lock",
		"async", "await", "as", "is", "out", "ref", "var", "delegate", "event",
		"continue", "break", "goto", "null", "true", "false", "void", "class",
		"interface", "struct", "enum", "record", "base", "this")

	ruby := []declRule{
		{kind: "class", re: regexp.MustCompile(`^\s*class\s+([A-Za-z_]\w*(?:::[A-Za-z_]\w*)*)`)},
		{kind: "module", re: regexp.MustCompile(`^\s*module\s+([A-Za-z_]\w*(?:::[A-Za-z_]\w*)*)`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*def\s+(?:self\.)?([A-Za-z_]\w*[!?=]?)`)},
	}
	rubyKw := kwSet("if", "unless", "while", "until", "for", "case", "when", "and", "or",
		"not", "def", "class", "module", "end", "begin", "rescue", "ensure", "return",
		"raise", "yield", "do", "then", "else", "elsif", "break", "next", "redo",
		"retry", "true", "false", "nil", "self", "super", "defined")

	php := []declRule{
		{kind: "class", re: regexp.MustCompile(`^\s*(?:abstract|final|readonly)?\s*class\s+([A-Za-z_]\w*)`)},
		{kind: "interface", re: regexp.MustCompile(`^\s*interface\s+([A-Za-z_]\w*)`)},
		{kind: "trait", re: regexp.MustCompile(`^\s*trait\s+([A-Za-z_]\w*)`)},
		{kind: "enum", re: regexp.MustCompile(`^\s*enum\s+([A-Za-z_]\w*)`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract)\s+)*function\s+([A-Za-z_]\w*)\s*\(`)},
		{kind: "const", re: regexp.MustCompile(`^\s*const\s+([A-Za-z_]\w*)\s*=`)},
	}
	phpKw := kwSet("if", "else", "elseif", "for", "foreach", "while", "switch", "return",
		"new", "throw", "try", "catch", "finally", "case", "function", "class",
		"interface", "trait", "use", "namespace", "include", "include_once", "require",
		"require_once", "echo", "print", "continue", "break", "goto", "declare",
		"isset", "unset", "empty", "null", "true", "false", "public", "private",
		"protected", "static", "final", "abstract", "global", "list", "array",
		"match", "fn", "enum")

	shell := []declRule{
		{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*(?:function\s+)?([A-Za-z_]\w*)\s*\(\)\s*\{`)},
		{kind: "func", isDef: true, re: regexp.MustCompile(`^\s*function\s+([A-Za-z_]\w*)\s*\{`)},
	}
	shKw := kwSet("if", "then", "while", "until", "for", "case", "do", "done", "fi",
		"esac", "function", "in", "select", "elif", "else", "time")

	css := []declRule{
		{kind: "class", re: regexp.MustCompile(`^\s*\.([A-Za-z_][\w-]*)`)},
		{kind: "const", re: regexp.MustCompile(`^\s*#([A-Za-z_][\w-]*)`)},
		{kind: "func", re: regexp.MustCompile(`^\s*@keyframes\s+([A-Za-z_][\w-]*)`)},
		{kind: "prop", re: regexp.MustCompile(`(--[A-Za-z_][\w-]*)\s*:`)},
	}
	cssKw := kwSet("import", "media", "supports", "font-face", "layer", "container",
		"scope", "property", "counter-style", "charset", "namespace", "page")

	html := []declRule{
		{kind: "const", re: regexp.MustCompile(`\bid="([A-Za-z_][\w-]*)"`)},
	}
	htmlKw := kwSet()

	markdown := []declRule{
		{kind: "heading", re: regexp.MustCompile(`^(#{1,6})\s+(.+)$`)},
	}
	mdKw := kwSet()

	json := []declRule{
		{kind: "prop", re: regexp.MustCompile(`"([A-Za-z_$][\w$.-]*)"\s*:`)},
	}
	jsonKw := kwSet()

	yaml := []declRule{
		{kind: "prop", re: regexp.MustCompile(`^([A-Za-z_][\w-]*):`)},
	}
	yamlKw := kwSet()

	specs = map[string]*langSpec{
		"javascript": {lineComment: []string{"//"}, block: "/*", backtick: true, rules: js, kw: jsKw},
		"typescript": {lineComment: []string{"//"}, block: "/*", backtick: true, rules: js, kw: jsKw},
		"python":     {indent: true, lineComment: []string{"#"}, triple: true, rules: python, kw: pyKw},
		"rust":       {lineComment: []string{"//"}, block: "/*", rules: rust, kw: rsKw},
		"c":          {lineComment: []string{"//"}, block: "/*", rules: cfam, kw: cfamKw},
		"cpp":        {lineComment: []string{"//"}, block: "/*", rules: cfam, kw: cfamKw},
		"java":       {lineComment: []string{"//"}, block: "/*", rules: java, kw: javaKw},
		"csharp":     {lineComment: []string{"//"}, block: "/*", rules: csharp, kw: csKw},
		"ruby":       {indent: true, lineComment: []string{"#"}, rules: ruby, kw: rubyKw},
		"php":        {lineComment: []string{"//", "#"}, block: "/*", rules: php, kw: phpKw},
		"shell":      {lineComment: []string{"#"}, rules: shell, kw: shKw},
		"css":        {lineComment: nil, block: "/*", rules: css, kw: cssKw},
		"html":       {lineComment: nil, block: "<!--", blockEnd: "-->", rules: html, kw: htmlKw},
		"markdown":   {lineComment: nil, block: "<!--", blockEnd: "-->", rules: markdown, kw: mdKw},
		"json":       {lineComment: []string{"//"}, block: "/*", rules: json, kw: jsonKw},
		"yaml":       {lineComment: []string{"#"}, rules: yaml, kw: yamlKw},
	}
}

var specs map[string]*langSpec

type stripState struct {
	inBlock  bool
	inTriple bool
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

func stripLine(ln string, spec *langSpec, st *stripState) string {
	s := ln
	if st.inBlock {
		if i := strings.Index(s, "*/"); i >= 0 {
			s = s[i+2:]
			st.inBlock = false
		} else {
			return ""
		}
	}
	if spec.block != "" {
		blockEnd := spec.blockEnd
		if blockEnd == "" {
			blockEnd = "*/"
		}
		for {
			start := strings.Index(s, spec.block)
			if start < 0 {
				break
			}
			end := strings.Index(s[start+len(spec.block):], blockEnd)
			if end < 0 {
				s = s[:start]
				st.inBlock = true
				break
			}
			s = s[:start] + s[start+len(spec.block)+end+len(blockEnd):]
		}
	}
	if spec.triple {
		for _, d := range []string{`"""`, `'''`} {
			if st.inTriple {
				if idx := strings.Index(s, d); idx >= 0 {
					s = s[idx+len(d):]
					st.inTriple = false
				} else {
					return ""
				}
			}
			for strings.Contains(s, d) {
				start := strings.Index(s, d)
				end := strings.Index(s[start+len(d):], d)
				if end < 0 {
					s = s[:start]
					st.inTriple = true
					break
				}
				s = s[:start] + s[start+len(d)+end+len(d):]
			}
		}
	}
	s = stripStringRe.ReplaceAllString(s, `""`)
	if spec.backtick {
		s = backtickRe.ReplaceAllString(s, "``")
	}
	for _, c := range spec.lineComment {
		if i := strings.Index(s, c); i >= 0 {
			s = s[:i]
			break
		}
	}
	return s
}

func braceDelta(clean string) (opens, closes int) {	for _, r := range clean {
		switch r {
		case '{':
			opens++
		case '}':
			closes++
		}
	}
	return
}

func leadingSpaces(s string) int {
	n := 0
	for n < len(s) && s[n] == ' ' {
		n++
	}
	return n
}

type typeDecl struct {
	sym     Symbol
	bodyEnd int // exclusive line index
}

// extractForeign extracts symbols and call edges from a non-Go source file
// using per-language lexical rules. It is heuristic, not a full parser.
func extractForeign(rel string, src []byte, lang string) ([]Symbol, map[string][]string, *Pkg, error) {
	src = sfcScript(rel, src)
	if len(bytes.TrimSpace(src)) == 0 {
		return nil, nil, nil, nil
	}
	spec := specs[lang]
	f := analyze(src, spec)
	calls := map[string][]string{}
	var syms []Symbol
	var types []typeDecl
	n := len(f.lines)

	for i := 0; i < n; i++ {
		trimmed := strings.TrimSpace(f.lines[i])
		if trimmed == "" || f.com[i] {
			continue
		}
		rule, m := matchRule(f.lines[i], spec)
		if rule == nil {
			continue
		}
		name := m[len(m)-1]
		sym := Symbol{
			Kind: rule.kind,
			Name: name,
			File: rel,
			Line: i + 1,
			Lang: lang,
		}
		bodyEnd := bodyEndFor(i, f, spec)
		if bodyEnd > 0 {
			sym.End = bodyEnd
		} else {
			sym.End = i + 1
		}
		if rule.recv > 0 {
			sym.Kind = "method"
			sym.Receiver = m[rule.recv]
		} else if rule.isDef {
			if recv := enclosingType(i, types); recv != "" {
				sym.Kind = "method"
				sym.Receiver = recv
			} else if sym.Kind == "method" {
				sym.Kind = "func"
			}
		}
		if rule.isDef {
			if bodyEnd > 0 {
				for j := i + 1; j < bodyEnd && j < n; j++ {
					scanCalls(f, j, sym.FullName(), calls, spec)
				}
			}
		} else {
			types = append(types, typeDecl{sym: sym, bodyEnd: bodyEnd})
		}
		syms = append(syms, sym)
	}

	dedupeCalls(calls)
	pkg := &Pkg{
		Name:    filepath.Base(filepath.Dir(rel)),
		Path:    filepath.Dir(rel),
		Files:   []string{rel},
		Lang:    lang,
	}
	return syms, calls, pkg, nil
}

func matchRule(line string, spec *langSpec) (*declRule, []string) {
	for i := range spec.rules {
		rule := &spec.rules[i]
		m := rule.re.FindStringSubmatch(line)
		if m == nil {
			continue
		}
		name := m[1]
		if spec.kw[name] {
			continue
		}
		return rule, m
	}
	return nil, nil
}

func bodyEndFor(i int, f *ffile, spec *langSpec) int {
	if spec.indent {
		base := f.indent[i]
		for j := i + 1; j < len(f.lines); j++ {
			if f.blank[j] || f.com[j] {
				continue
			}
			if f.indent[j] <= base {
				return j
			}
		}
		return len(f.lines)
	}
	base := f.preD[i]
	for j := i + 1; j < len(f.lines); j++ {
		if f.postD[j] <= base {
			return j
		}
	}
	return len(f.lines)
}

func enclosingType(line int, types []typeDecl) string {
	for i := len(types) - 1; i >= 0; i-- {
		t := types[i]
		if !typeKinds[t.sym.Kind] {
			continue
		}
		if line >= t.sym.Line && (t.bodyEnd == 0 || line < t.bodyEnd) {
			return t.sym.Name
		}
	}
	return ""
}

func scanCalls(f *ffile, i int, owner string, calls map[string][]string, spec *langSpec) {
	line := f.lines[i]
	trimmed := strings.TrimSpace(line)
	if trimmed == "" || f.com[i] {
		return
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "@") {
		return
	}
	for r := range spec.rules {
		rule := &spec.rules[r]
		if rule.isDef || typeKinds[rule.kind] {
			if rule.re.MatchString(line) {
				return
			}
		}
	}
	for _, m := range callRe.FindAllStringSubmatch(f.clean[i], -1) {
		first := m[1]
		full := strings.TrimSpace(strings.TrimSuffix(m[0], "("))
		if spec.kw[first] {
			if !strings.Contains(full, ".") {
				continue
			}
			last := full[strings.LastIndexByte(full, '.')+1:]
			if spec.kw[last] {
				continue
			}
			full = last
		}
		if full == owner {
			continue
		}
		if len(full) > 80 {
			full = full[:80]
		}
		calls[owner] = append(calls[owner], full)
	}
}

func dedupeCalls(calls map[string][]string) {
	for k, v := range calls {
		seen := map[string]bool{}
		out := v[:0]
		for _, c := range v {
			if !seen[c] {
				seen[c] = true
				out = append(out, c)
			}
		}
		calls[k] = out
	}
}
