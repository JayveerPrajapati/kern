package code

// Content-based language detection and path-less folding. Fold normally needs
// a path so DetectLanguage can map the file extension to a language; the two
// raw-source budget surfaces (kern_context_budget MCP tool, kern budget CLI)
// only have bytes, so FitCode folds through FoldContent instead. Detection is
// deliberately conservative: only strong structural markers qualify, and any
// ambiguity resolves to "" so non-code text is never mangled. Detection errors
// are also harmless downstream: foldLines only folds when it finds a real
// function-like body, so a wrong language degrades to a no-op.

import (
	"regexp"
	"strings"
)

// maxSniffLines caps how much content is examined during detection so sniffing
// stays cheap even for very large inputs.
const maxSniffLines = 200

var (
	goPkgRe   = regexp.MustCompile(`^package\s+\w+`)
	goDeclRe  = regexp.MustCompile(`^(?:func|type|import)\s`)
	javaRe    = regexp.MustCompile(`^\s*public\s+(?:class|interface|record|enum)\s+\w+|^\s*(?:class|interface)\s+\w+\s*\{`)
	cRe       = regexp.MustCompile(`^\s*#\s*(?:include|define)\b`)
	rustRe    = regexp.MustCompile(`^\s*(?:pub\s+)?fn\s+\w+|^\s*use\s+[a-zA-Z0-9_:]+::|^\s*impl\s`)
	jsRe      = regexp.MustCompile(`^\s*(?:export\s+)?(?:function|const|let|var)\s+\w+|^\s*import\s+.+from\s+['"]|^\s*const\s+\w+\s*=\s*\(`)
	pythonRe  = regexp.MustCompile(`^\s*(?:def|class)\s+\w+|^\s*(?:import|from)\s+\w`)
	rubyReqRe = regexp.MustCompile(`^\s*require\s+['"]`)
	rubyDefRe = regexp.MustCompile(`^\s*def\s+\w+`)
	rubyEndRe = regexp.MustCompile(`^\s*end\s*$`)
	shellRe   = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]*\(\)\s*\{`)
)

// DetectExtFromContent sniffs file content and returns a file extension key
// (as understood by DetectLanguage) for a foldable language: "go", "py", "js",
// "java", "c", "rs", "rb" or "sh". It returns "" for anything that is not
// confidently source code (prose, logs, yaml, css, ...) so callers can leave
// such text untouched. Only the first ~200 lines are examined.
//
// Signals are strong structural markers, evaluated in priority order: a
// shebang decides immediately; then multi-marker languages (go: package header
// plus a func/type/import line; ruby: def plus a matching end) are confirmed
// from the whole sample; single-marker languages are resolved in the order
// java, c, rust, js, python, ruby, shell.
func DetectExtFromContent(content []byte) string {
	lines := strings.Split(string(content), "\n")
	if len(lines) > maxSniffLines {
		lines = lines[:maxSniffLines]
	}
	if len(lines) == 0 {
		return ""
	}
	// Shebang is the strongest available signal: decide immediately.
	if first := strings.TrimSpace(lines[0]); strings.HasPrefix(first, "#!") {
		switch {
		case strings.Contains(first, "python"):
			return "py"
		case strings.Contains(first, "ruby"):
			return "rb"
		case strings.Contains(first, "sh") || strings.Contains(first, "bash") || strings.Contains(first, "zsh"):
			return "sh"
		}
	}

	var goPkg, goDecl, java, c, rust, js, python, ruby, rbDef, rbEnd, shell bool
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" {
			continue
		}
		if !goPkg && goPkgRe.MatchString(trimmed) {
			goPkg = true
		}
		if !goDecl && goDeclRe.MatchString(trimmed) {
			goDecl = true
		}
		if javaRe.MatchString(trimmed) {
			java = true
		}
		if cRe.MatchString(trimmed) {
			c = true
		}
		if rustRe.MatchString(trimmed) {
			rust = true
		}
		if jsRe.MatchString(trimmed) {
			js = true
		}
		if pythonRe.MatchString(trimmed) {
			python = true
		}
		if rubyReqRe.MatchString(trimmed) {
			ruby = true
		}
		if rubyDefRe.MatchString(trimmed) {
			rbDef = true
		}
		if rubyEndRe.MatchString(trimmed) {
			rbEnd = true
		}
		if shellRe.MatchString(trimmed) {
			shell = true
		}
	}

	// Priority resolution in detection order. go and ruby need both halves of
	// their marker pair, so they cannot fire on a single stray line.
	switch {
	case goPkg && goDecl:
		return "go"
	case java:
		return "java"
	case c:
		return "c"
	case rust:
		return "rs"
	case js:
		return "js"
	case python:
		return "py"
	case ruby || (rbDef && rbEnd):
		return "rb"
	case shell:
		return "sh"
	}
	return ""
}

// FoldContent folds function/method bodies of content whose language can be
// detected from the bytes alone (no path needed). It is the path-less twin of
// Fold: the detected extension is used to build the synthetic path that Fold
// keys language detection off. Unknown or ambiguous content is returned
// unchanged, and even a misdetection degrades to a no-op because the line fold
// only rewrites real function-like bodies.
func FoldContent(content []byte) []byte {
	ext := DetectExtFromContent(content)
	if ext == "" {
		return content
	}
	return Fold("content."+ext, content)
}
