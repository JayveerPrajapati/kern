package index

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"
)

// stripLine removes comments, string literals and triple-quoted spans from a
// line, returning the code that remains. State carried across lines (an open
// block comment or triple string) lives in st. Scanning is left-to-right so a
// comment opener inside a string (var s = "/*") or vice-versa is consumed by
// its real container and never starts a bogus span.
func stripLine(ln string, spec *langSpec, st *stripState) string {
	// Finish a block comment opened on a previous line.
	if st.inBlock {
		j := strings.Index(ln, "*/")
		if j < 0 {
			return ""
		}
		ln = ln[j+2:]
		st.inBlock = false
	}
	// Finish a triple string opened on a previous line.
	if spec.triple && st.inTriple != "" {
		j := strings.Index(ln, st.inTriple)
		if j < 0 {
			return ""
		}
		ln = ln[j+len(st.inTriple):]
		st.inTriple = ""
	}
	// Finish a Ruby heredoc opened on a previous line. The terminator
	// must appear on its own line (trimmed). Everything inside the heredoc
	// body is dropped so def/class rules never match fake code.
	if spec.heredoc && st.inHeredoc != "" {
		if strings.TrimSpace(ln) == st.inHeredoc {
			st.inHeredoc = ""
			return ""
		}
		return ""
	}
	blockEnd := spec.blockEnd
	if blockEnd == "" {
		blockEnd = "*/"
	}
	var b strings.Builder
	i := 0
	start := 0 // start of code not yet appended
	drop := func(end int) {
		b.WriteString(ln[start:i])
		i = end
		start = end
	}
	finish := func() string {
		b.WriteString(ln[start:])
		return b.String()
	}
	n := len(ln)
	for i < n {
		// Line comment: the rest of the line is dropped.
		for _, c := range spec.lineComment {
			if c != "" && strings.HasPrefix(ln[i:], c) {
				b.WriteString(ln[start:i])
				return b.String()
			}
		}
		// Block comment.
		if spec.block != "" && strings.HasPrefix(ln[i:], spec.block) {
			if e := strings.Index(ln[i+len(spec.block):], blockEnd); e >= 0 {
				drop(i + len(spec.block) + e + len(blockEnd))
				continue
			}
			b.WriteString(ln[start:i])
			st.inBlock = true
			return b.String()
		}
		// Triple-quoted string (Python-style).
		if spec.triple {
			matched := false
			for _, d := range []string{`"""`, `'''`} {
				if strings.HasPrefix(ln[i:], d) {
					matched = true
					if e := strings.Index(ln[i+len(d):], d); e >= 0 {
						drop(i + len(d) + e + len(d))
					} else {
						b.WriteString(ln[start:i])
						st.inTriple = d
						return b.String()
					}
					break
				}
			}
			if matched {
				continue
			}
		}
		// Plain string or char literal, with backslash escapes.
		if c := ln[i]; c == '"' || c == '\'' {
			j := i + 1
			for j < n {
				if ln[j] == '\\' {
					j += 2
					if j > n {
						j = n
					}
					continue
				}
				if ln[j] == c {
					break
				}
				j++
			}
			if j >= n {
				// Unterminated on this line: treat the rest as a string so a
				// stray `/*`, `//` or quote inside it can't poison the scan.
				b.WriteString(ln[start:i])
				return b.String()
			}
			drop(j + 1)
			continue
		}
		// Template literal (JavaScript/TypeScript).
		if spec.backtick && ln[i] == '`' {
			if e := strings.IndexByte(ln[i+1:], '`'); e >= 0 {
				drop(i + 1 + e + 1)
				continue
			}
			b.WriteString(ln[start:i])
			return b.String()
		}
		// Ruby heredoc opener: <<IDENT, <<-IDENT, <<~IDENT.
		// Not preceded by an identifier char to avoid matching left-shift
		// (x << y, where the regex won't match due to the space, but
		// x <<y without space is still treated as heredoc — acceptable
		// edge case).
		if spec.heredoc && ln[i] == '<' && i+1 < n && ln[i+1] == '<' &&
			(i == 0 || !isIdentChar(ln[i-1])) {
			if m := heredocStartRe.FindStringSubmatch(ln[i:]); m != nil {
				b.WriteString(ln[start:i])
				st.inHeredoc = m[2]
				return b.String()
			}
		}
		i++
	}
	return finish()
}

func braceDelta(clean string) (opens, closes int) {
	for _, r := range clean {
		switch r {
		case '{':
			opens++
		case '}':
			closes++
		}
	}
	return
}

func isIdentChar(c byte) bool {
	return c == '_' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9')
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

// extractForeign extracts symbols, call edges and inheritance edges from a
// non-Go source file. When built with -tags treesitter, it uses tree-sitter
// for precise AST-based extraction; otherwise it falls back to regex-based
// heuristics.

var (
	// reJavaImport matches a single-line Java import: optional `static`,
	// then a dotted chain of identifiers optionally ending in `.*`. Group 1
	// is the full dotted chain (wildcard included).
	reJavaImport = regexp.MustCompile(`^\s*import\s+(?:static\s+)?([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*|\.[*])*)\s*;`)
)

// foreignImports extracts package imports for foreign languages whose import
// syntax is a simple single-line statement (currently Java). Go imports are
// extracted by goast.go; other languages are not yet covered. Returns nil
// when the language has no import extraction.
func foreignImports(src []byte, lang string) []ImportEdge {
	switch lang {
	case "java":
		var out []ImportEdge
		for _, ln := range bytes.Split(src, []byte("\n")) {
			m := reJavaImport.FindSubmatch(ln)
			if m == nil {
				continue
			}
			line := string(ln)
			isStatic := strings.HasPrefix(strings.TrimSpace(line), "import static")
			if pkg := javaPackagePath(string(m[1]), isStatic); pkg != "" {
				// Java import statements are single-line, anchored and
				// comment-stripped: as reliable as the Go AST imports.
				out = append(out, ImportEdge{Path: pkg, Confidence: ConfidenceHigh})
			}
		}
		if len(out) == 0 {
			return nil
		}
		return out
	}
	return nil
}

// javaPackagePath normalizes a Java import to its package path so boundary
// rules match against directories, not types:
//   - "a.b.*" (wildcard) is already a package.
//   - "a.b.C" names a type; the trailing type segment is dropped.
//   - "a.b.C.m" (static member) drops the member and the type.
func javaPackagePath(full string, isStatic bool) string {
	if strings.HasSuffix(full, ".*") {
		return strings.TrimSuffix(full, ".*")
	}
	n := 1
	if isStatic {
		n = 2
	}
	for i := 0; i < n; i++ {
		if j := strings.LastIndexByte(full, '.'); j > 0 {
			full = full[:j]
		}
	}
	return full
}

func extractForeign(rel string, src []byte, lang string) ([]Symbol, map[string][]CallEdge, map[string][]string, *Pkg, error) {
	// Try tree-sitter first if available. Tree-sitter handles definitions and
	// call edges precisely; entry points (routes, annotations) still come from
	// the regex entry rules, so merge them in to keep routes searchable.
	imports := foreignImports(src, lang)
	//nolint:staticcheck // SA4023: tree-sitter branch only reachable with -tags treesitter
	if syms, calls, inherits, pkg, err := tsExtract(rel, src, lang); err == nil {
		if pkg != nil {
			pkg.Imports = imports
		}
		src = sfcScript(rel, src)
		if len(bytes.TrimSpace(src)) != 0 {
			spec := specs[lang]
			if spec == nil {
				return syms, calls, inherits, pkg, nil
			}
			f := analyze(src, spec)
			// Rebuild the type table from tree-sitter symbols so entry rules can
			// resolve method receivers (UserController.list) the same way the
			// regex path does.
			var types []typeDecl
			for i := range syms {
				if typeKinds[syms[i].Kind] {
					types = append(types, typeDecl{sym: syms[i], bodyEnd: syms[i].End})
				}
			}
			syms = append(syms, extractEntries(f, spec, types, syms, rel, lang)...)
		}
		return syms, calls, inherits, pkg, nil
	}

	// Fallback to regex-based extraction.
	return extractForeignRegex(rel, src, lang)
}

// extractForeignRegex is the pure regex/heuristic extractor for foreign
// languages — the default path when tree-sitter is not compiled in. It is
// split out of extractForeign so the parity gate (internal/index/parity_test.go,
// -tags treesitter) can compare it directly against tsExtract on the shared
// fixture corpus: both extractors must agree on symbol sets, edge sets and
// confidences, with only documented tolerances where regex legitimately
// degrades.
func extractForeignRegex(rel string, src []byte, lang string) ([]Symbol, map[string][]CallEdge, map[string][]string, *Pkg, error) {
	imports := foreignImports(src, lang)
	src = sfcScript(rel, src)
	if len(bytes.TrimSpace(src)) == 0 {
		return nil, nil, nil, nil, nil
	}
	spec := specs[lang]
	if spec == nil {
		return nil, nil, nil, nil, nil
	}
	f := analyze(src, spec)
	calls := map[string][]CallEdge{}
	inherits := map[string][]string{}
	var syms []Symbol
	var types []typeDecl
	n := len(f.lines)

	for i := 0; i < n; i++ {
		trimmed := strings.TrimSpace(f.lines[i])
		if trimmed == "" || f.com[i] {
			continue
		}
		// Skip lines whose stripped form is empty (code inside heredocs,
		// block comments, or triple strings produces no clean code).
		if strings.TrimSpace(f.clean[i]) == "" {
			continue
		}
		rule, m := matchRule(f.lines[i], spec)
		if rule == nil {
			continue
		}
		name := m[len(m)-1]
		sym := Symbol{
			Kind:       rule.kind,
			Name:       name,
			File:       rel,
			Line:       i + 1,
			Lang:       lang,
			Confidence: ConfidenceHigh,
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
		// Extract inheritance for type declarations (class/interface/enum).
		if typeKinds[rule.kind] {
			scanInheritsRegex(f, i, bodyEnd, name, lang, inherits)
		}
		if rule.isDef {
			if bodyEnd > 0 {
				// Java declares types explicitly, so per-method local-type
				// tracking + callee resolution promote Java to "resolved"
				// precision: v.method() is recorded as Type.method() and binds
				// cross-file against symbols by name, mirroring Go's
				// resolveCallee. All other languages keep the name-heuristic
				// path (lt == nil).
				var lt map[string]string
				if lang == "java" {
					lt = collectJavaLocalTypes(f, i, bodyEnd, spec)
				}
				for j := i; j < bodyEnd && j < n; j++ {
					if lt != nil {
						scanCallsResolved(f, j, sym.FullName(), calls, spec, lt)
					} else {
						scanCalls(f, j, sym.FullName(), calls, spec)
					}
				}
			}
		} else {
			types = append(types, typeDecl{sym: sym, bodyEnd: bodyEnd})
		}
		syms = append(syms, sym)
	}

	syms = append(syms, extractEntries(f, spec, types, syms, rel, lang)...)
	dedupeCalls(calls)
	pkg := &Pkg{
		Name:    filepath.Base(filepath.Dir(rel)),
		Path:    filepath.Dir(rel),
		Files:   []string{rel},
		Lang:    lang,
		Imports: imports,
	}
	calls = attributeTopLevelCalls(rel, src, lang, syms, calls)
	return syms, calls, inherits, pkg, nil
}

// attributeTopLevelCalls records calls that occur OUTSIDE any symbol body -
// top-level statements and anonymous callbacks such as
// el.addEventListener("evt", () => handler()) - with the file itself as the
// caller, keyed "file:<rel>". That key matches the graph's file-node ID
// exactly, so caller traversals (WhoCalls, WhatDependsOn, what-if) resolve
// it instead of reporting the callee as isolated: without this edge,
// removing a handler wired only via an event callback was called "isolated:
// safe to proceed" on the app's only fetch path (e2e round 2, P0-2).
// Shared by the regex and tree-sitter extractors so the parity gate stays
// balanced: both sides gain identical file-owned edges.
func attributeTopLevelCalls(rel string, src []byte, lang string, syms []Symbol, calls map[string][]CallEdge) map[string][]CallEdge {
	spec := specs[lang]
	if spec == nil || calls == nil {
		return calls
	}
	src = sfcScript(rel, src)
	if len(bytes.TrimSpace(src)) == 0 {
		return calls
	}
	f := analyze(src, spec)
	n := len(f.lines)
	covered := make([]bool, n)
	for _, s := range syms {
		// Only def-like symbols (the kinds whose bodies the main scan loop
		// attributes calls to) cover lines. A top-level const's brace-depth
		// End can swallow following callback lines; those must stay
		// unattributed so the file owns their calls.
		if s.File != rel || s.End < s.Line || (s.Kind != "func" && s.Kind != "method") {
			continue
		}
		for j := s.Line - 1; j < s.End && j < n; j++ {
			covered[j] = true
		}
	}
	owner := "file:" + rel
	for j := 0; j < n; j++ {
		if covered[j] || f.blank[j] || f.com[j] || strings.TrimSpace(f.clean[j]) == "" {
			continue
		}
		// Type declaration lines ("class Child(Greeter):") are not statements:
		// their parenthesized bases must not read as file-owned calls.
		if rule, _ := matchRule(f.lines[j], spec); rule != nil && typeKinds[rule.kind] {
			continue
		}
		scanCalls(f, j, owner, calls, spec)
	}
	dedupeCalls(calls)
	return calls
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
	// A self-contained declaration line — balanced braces with an opening
	// brace on the line itself (single-line JS/TS functions like
	// "export function useAuth() { return {...} }") — has no separate
	// body: the depth scan below would otherwise mistake a LATER line's
	// opening brace (postD > base) for this symbol's body and swallow the
	// rest of the file, attributing every subsequent call to every
	// earlier single-line function (V8 spurious edges).
	if f.postD[i] == base && strings.Contains(f.clean[i], "{") {
		return i + 1
	}
	// The body opens when depth first exceeds base. If the opening brace is
	// on the declaration line itself (postD[i] > base), the body starts
	// here. Otherwise the brace may be on a subsequent line (e.g.
	// "class Foo\nextends Bar\n{") and we must skip past header lines
	// until depth exceeds base.
	started := f.postD[i] > base
	for j := i + 1; j < len(f.lines); j++ {
		if !started && f.postD[j] > base {
			started = true
		}
		if started && f.postD[j] <= base {
			return j
		}
	}
	if started {
		return len(f.lines)
	}
	return i + 1
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

func scanCalls(f *ffile, i int, owner string, calls map[string][]CallEdge, spec *langSpec) {
	scanCallsInner(f, i, owner, calls, spec, nil)
}

// scanCallsResolved is scanCalls with receiver-var calls rewritten through a
// per-method local-types map before recording: v.method(...) is recorded as
// Type.method(...) when v is a known local of type Type. It drives Java's
// "resolved" precision tier (see java_resolve.go).
func scanCallsResolved(f *ffile, i int, owner string, calls map[string][]CallEdge, spec *langSpec, lt map[string]string) {
	scanCallsInner(f, i, owner, calls, spec, lt)
}

func scanCallsInner(f *ffile, i int, owner string, calls map[string][]CallEdge, spec *langSpec, lt map[string]string) {
	trimmed := strings.TrimSpace(f.lines[i])
	if trimmed == "" || f.com[i] {
		return
	}
	if strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "@") {
		return
	}
	// We intentionally do NOT early-return on declaration lines. A line can
	// contain both a declaration and calls (e.g. foo(function cb(){})), and
	// same-line bodies (function foo() { return bar() }) have calls on the
	// decl line. The self-call check below (full == owner) prevents the
	// declared name itself from being recorded, and keywords are filtered
	// separately.
	for _, m := range callRe.FindAllStringSubmatch(f.clean[i], -1) {
		first := m[1]
		full := strings.TrimSpace(strings.TrimSuffix(m[0], "("))
		// callRe tolerates generic type arguments between the callee and the
		// paren (useInfiniteData<UserDto>(...)); drop the <...> part so the
		// recorded callee is the bare name and resolves like a non-generic
		// call. No-op for plain calls.
		full = stripGenerics(full)
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
		// Resolve a known receiver variable to its declared type before the
		// self-call check so a call through a local (app.run()) is still seen
		// as a self-call. A nil lt is a no-op for other languages.
		if lt != nil {
			full = resolveJavaCallee(full, lt)
		}
		if full == owner {
			continue
		}
		if len(full) > 80 {
			full = full[:80]
		}
		// Regex-extracted calls are name-heuristic: the pattern can match
		// inside strings/edge cases, and callees are never type-resolved
		// except through the Java local-types tier. MEDIUM either way.
		calls[owner] = append(calls[owner], CallEdge{Target: full, Confidence: ConfidenceMedium})
	}
}

func dedupeCalls(calls map[string][]CallEdge) {
	for k, v := range calls {
		seen := map[string]bool{}
		out := v[:0]
		for _, c := range v {
			if !seen[c.Target] {
				seen[c.Target] = true
				out = append(out, c)
			}
		}
		calls[k] = out
	}
}

// reJavaExtends matches "extends Foo" in a Java type declaration.
var reJavaExtends = regexp.MustCompile(`\bextends\s+([\w.]+)`)

// reJavaImplements matches "implements Foo, Bar" in a Java type declaration.
var reJavaImplements = regexp.MustCompile(`\bimplements\s+([\w.,\s]+?)(?:\s*(?:\{|$))`)

// rePythonBases matches "class Cat(Animal, Pet):" bases in Python.
var rePythonBases = regexp.MustCompile(`^\s*class\s+\w+\s*\(([^)]+)\)`)

// reRubyBase matches "class Cat < Animal".
var reRubyBase = regexp.MustCompile(`^\s*class\s+\w+\s*<\s*([\w:]+)`)

// reCSharpBase matches "class Cat : Animal, Pet" in C#.
var reCSharpBase = regexp.MustCompile(`:\s*([\w.,\s]+?)(?:\s*(?:\{|$))`)

// reTSExtends matches "extends Foo" in TypeScript/JavaScript.
var reTSExtends = regexp.MustCompile(`\bextends\s+([\w.]+)`)

// reTSImplements matches "implements Foo, Bar" in TypeScript.
var reTSImplements = regexp.MustCompile(`\bimplements\s+([\w.,\s]+?)(?:\s*(?:\{|$))`)

// rePhpExtends matches "extends Foo" in PHP.
var rePhpExtends = regexp.MustCompile(`\bextends\s+([\w\\]+)`)

// rePhpImplements matches "implements Foo, Bar" in PHP.
var rePhpImplements = regexp.MustCompile(`\bimplements\s+([\w.,\\\s]+?)(?:\s*(?:\{|$))`)

// scanInheritsRegex extracts extends/implements edges from a type declaration
// using regex heuristics. It scans the declaration line and the next few lines
// (up to the opening brace) for inheritance clauses. This is the regex
// fallback for when tree-sitter is not compiled in.
func scanInheritsRegex(f *ffile, declLine, bodyEnd int, typeName, lang string, inherits map[string][]string) {
	// Collect the declaration header: the declaration line plus any
	// continuation lines up to the opening brace (Java/C#/PHP may split
	// "class Foo\n  extends Bar\n  implements Baz {" across lines).
	var header []string
	for j := declLine; j < len(f.lines) && j < bodyEnd; j++ {
		trimmed := strings.TrimSpace(f.lines[j])
		if trimmed == "" || f.com[j] {
			continue
		}
		header = append(header, f.clean[j])
		// Stop once we see the opening brace — the class body starts here.
		if strings.Contains(f.clean[j], "{") {
			break
		}
	}
	fullHeader := strings.Join(header, " ")
	cleanHeader := stripGenerics(fullHeader)

	var bases []string
	switch lang {
	case "java":
		if m := reJavaExtends.FindStringSubmatch(cleanHeader); m != nil {
			bases = append(bases, "extends:"+baseName(m[1]))
		}
		if m := reJavaImplements.FindStringSubmatch(cleanHeader); m != nil {
			for _, b := range strings.Split(m[1], ",") {
				b = strings.TrimSpace(b)
				if b != "" {
					bases = append(bases, "implements:"+baseName(b))
				}
			}
		}
	case "typescript", "javascript":
		if m := reTSExtends.FindStringSubmatch(cleanHeader); m != nil {
			bases = append(bases, "extends:"+baseName(m[1]))
		}
		if m := reTSImplements.FindStringSubmatch(cleanHeader); m != nil {
			for _, b := range strings.Split(m[1], ",") {
				b = strings.TrimSpace(b)
				if b != "" {
					bases = append(bases, "implements:"+baseName(b))
				}
			}
		}
	case "python":
		if m := rePythonBases.FindStringSubmatch(fullHeader); m != nil {
			for _, b := range strings.Split(m[1], ",") {
				b = strings.TrimSpace(b)
				if b != "" && b != "object" {
					bases = append(bases, "extends:"+baseName(b))
				}
			}
		}
	case "ruby":
		if m := reRubyBase.FindStringSubmatch(fullHeader); m != nil {
			bases = append(bases, "extends:"+baseName(m[1]))
		}
	case "csharp":
		if m := reCSharpBase.FindStringSubmatch(cleanHeader); m != nil {
			for _, b := range strings.Split(m[1], ",") {
				b = strings.TrimSpace(b)
				if b != "" {
					bases = append(bases, "extends:"+baseName(b))
				}
			}
		}
	case "php":
		if m := rePhpExtends.FindStringSubmatch(fullHeader); m != nil {
			bases = append(bases, "extends:"+baseName(m[1]))
		}
		if m := rePhpImplements.FindStringSubmatch(fullHeader); m != nil {
			for _, b := range strings.Split(m[1], ",") {
				b = strings.TrimSpace(b)
				if b != "" {
					bases = append(bases, "implements:"+baseName(b))
				}
			}
		}
	}
	if len(bases) > 0 {
		inherits[typeName] = append(inherits[typeName], bases...)
	}
}

// baseName extracts the last component of a qualified name
// (e.g. "com.example.Foo" -> "Foo", "Bar" -> "Bar").
func baseName(qualified string) string {
	if i := strings.LastIndexByte(qualified, '.'); i >= 0 {
		return qualified[i+1:]
	}
	return qualified
}

// stripGenerics removes <...> generic type parameters from a declaration header,
// handling nested angle brackets, so inheritance regexes match clean identifier chains.
func stripGenerics(s string) string {
	var b strings.Builder
	depth := 0
	for _, r := range s {
		switch r {
		case '<':
			depth++
		case '>':
			if depth > 0 {
				depth--
			}
		default:
			if depth == 0 {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}
