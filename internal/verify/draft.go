package verify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"go/ast"
	"go/parser"
	"go/scanner"
	"go/token"
	"maps"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/JayveerPrajapati/kern/internal/index"
)

// DraftFinding is one validation result for a draft code snippet.
type DraftFinding struct {
	Line    int    `json:"line"`
	Kind    string `json:"kind"` // "parse_error" | "unknown_import" | "unknown_symbol" | "unknown_method"
	Message string `json:"message"`
}

// goBuiltins are Go predeclared identifiers that are valid call targets
// without being declared in the draft or indexed in the project.
var goBuiltins = map[string]bool{
	"append": true, "cap": true, "clear": true, "close": true, "complex": true,
	"copy": true, "delete": true, "imag": true, "len": true, "make": true,
	"max": true, "min": true, "new": true, "panic": true, "print": true,
	"println": true, "real": true, "recover": true,
}

// Java/Python/JS draft-check regexes, compiled once at package init instead
// of per CheckDraft call.
var (
	// javaClassRe matches a class/interface/enum/record declaration header.
	javaClassRe = regexp.MustCompile(`\b(?:class|interface|enum|record)\s+([A-Za-z_$][\w$]*)`)
	// javaMethodRe matches a method declaration header.
	javaMethodRe = regexp.MustCompile(`(?:public|protected|private|static|final|\s)*\b(?:[A-Za-z_$][\w$]*|<[^>]+>|\[\]|\s)+\s+([A-Za-z_$][\w$]*)\s*\([^)]*\)\s*\{?`)
	// javaVarRe matches a variable declaration.
	javaVarRe = regexp.MustCompile(`\b([A-Za-z_$][\w$]*(?:<[^>]*>)?)\s+([A-Za-z_$][\w$]*)\s*(?:=|;|,|\))`)
	// javaCallRe matches a call target (possibly dotted).
	javaCallRe = regexp.MustCompile(`\b([A-Za-z_$][\w$]*(?:\.[A-Za-z_$][\w$]*)*)\s*\(`)
	// pyRelImportRe matches a Python relative import line.
	pyRelImportRe = regexp.MustCompile(`^\s*from\s+(\.+[a-zA-Z0-9_.]*)\s+import`)
	// pyColonHeaderRe matches a Python header line that must end with ':'.
	pyColonHeaderRe = regexp.MustCompile(`^\s*(?:def\s+[a-zA-Z_]\w*\s*\(.*?\)|class\s+[a-zA-Z_]\w*(?:\(.*?\))?|if\s+.*|elif\s+.*|while\s+.*|for\s+.*|with\s+.*)\s*$`)
	// jsRelImportRe matches a JS/TS relative import or require.
	jsRelImportRe = regexp.MustCompile(`(?:import\s+.*?from\s+['"](\.[^'"]+)['"]|require\(['"](\.[^'"]+)['"]\))`)
)

// CheckDraft validates a draft code snippet against the project index.
// Go code (lang "" or "go") is parsed with go/parser and checked
// structurally; Java code is checked for unresolved call targets;
// other languages get conservative checks only. Deterministic — no LLM.
func CheckDraft(ix *index.Index, root string, code []byte, lang string) []DraftFinding {
	if lang == "java" || (lang == "" && looksLikeJava(code)) {
		return checkJavaDraft(ix, root, code)
	}
	if lang == "python" || lang == "py" || (lang == "" && looksLikePython(code)) {
		return checkPythonDraft(ix, root, code)
	}
	if lang == "typescript" || lang == "ts" || lang == "javascript" || lang == "js" || (lang == "" && looksLikeJS(code)) {
		return checkJSDraft(ix, root, code)
	}
	if lang == "json" || (lang == "" && looksLikeJSON(code)) {
		return checkJSONDraft(code)
	}
	// Non-Go languages other than the above are skipped conservatively.
	if lang != "" && lang != "go" {
		return nil
	}
	fset := token.NewFileSet()
	f, err := parser.ParseFile(fset, "draft.go", code, 0)
	if err != nil {
		return []DraftFinding{parseFinding(err)}
	}

	var findings []DraftFinding

	// Import checks: relative imports must resolve to a real directory under
	// root; absolute imports are skipped (stdlib vs third-party needs module
	// resolution). Also record each import's alias for selector-call checks.
	aliases := map[string]string{} // import alias/last segment -> import path
	for _, imp := range f.Imports {
		path := strings.Trim(imp.Path.Value, `"`)
		line := fset.Position(imp.Pos()).Line
		if strings.HasPrefix(path, ".") {
			if _, serr := os.Stat(filepath.Join(root, path)); serr != nil {
				findings = append(findings, DraftFinding{
					Line:    line,
					Kind:    "unknown_import",
					Message: fmt.Sprintf("relative import %q does not exist under root", path),
				})
			}
		}
		aliases[importBaseName(imp, path)] = path
	}

	// Local symbol set: every name declared anywhere in the draft.
	locals := collectLocalSymbols(f)

	// Call-target checks, in AST order.
	ast.Inspect(f, func(n ast.Node) bool {
		ce, ok := n.(*ast.CallExpr)
		if !ok {
			return true
		}
		line := fset.Position(ce.Pos()).Line
		switch fun := ce.Fun.(type) {
		case *ast.Ident:
			// Simple call: builtin, local or indexed symbol, else unknown.
			if goBuiltins[fun.Name] || locals[fun.Name] || indexHasSymbol(ix, fun.Name) {
				return true
			}
			findings = append(findings, DraftFinding{
				Line:    line,
				Kind:    "unknown_symbol",
				Message: fmt.Sprintf("call to unknown symbol %q", fun.Name),
			})
		case *ast.SelectorExpr:
			// Selector call on an import alias: verify the method exists in
			// the indexed package. Selector calls on variables/receivers need
			// type resolution and are skipped.
			x, ok := fun.X.(*ast.Ident)
			if !ok {
				return true
			}
			path, isAlias := aliases[x.Name]
			if !isAlias {
				return true
			}
			method := fun.Sel.Name
			syms, known := indexPackageSymbols(ix, path)
			if !known {
				return true // package unknown to the index (e.g. stdlib) — skip
			}
			for _, s := range syms {
				if s.Name == method {
					return true
				}
			}
			findings = append(findings, DraftFinding{
				Line:    line,
				Kind:    "unknown_method",
				Message: fmt.Sprintf("%q not found in index", x.Name+"."+method),
			})
		}
		return true
	})

	return findings
}

// parseFinding builds a parse_error finding, extracting the error position
// line when the parser provides one.
func parseFinding(err error) DraftFinding {
	line := 0
	if el, ok := err.(scanner.ErrorList); ok && len(el) > 0 {
		line = el[0].Pos.Line
	} else if se, ok := err.(*scanner.Error); ok {
		line = se.Pos.Line
	}
	return DraftFinding{Kind: "parse_error", Line: line, Message: err.Error()}
}

// importBaseName returns the name an import is referenced by in the file: an
// explicit alias, or the last path segment.
func importBaseName(imp *ast.ImportSpec, path string) string {
	if imp.Name != nil {
		return imp.Name.Name
	}
	if i := strings.LastIndex(path, "/"); i >= 0 {
		return path[i+1:]
	}
	return path
}

// collectLocalSymbols returns the set of every name declared in the file:
// functions, methods (qualified "Recv.Method"), types, vars/consts,
// parameters, named results, and := / var locals inside bodies. Scope is not
// tracked in v1 — the whole file forms one set.
func collectLocalSymbols(f *ast.File) map[string]bool {
	locals := map[string]bool{}
	ast.Inspect(f, func(n ast.Node) bool {
		switch d := n.(type) {
		case *ast.FuncDecl:
			locals[d.Name.Name] = true
			if d.Recv != nil {
				for _, rf := range d.Recv.List {
					if id, ok := rf.Type.(*ast.Ident); ok {
						locals[id.Name+"."+d.Name.Name] = true
					}
				}
			}
			if d.Type != nil {
				addFieldNames(d.Type.Params, locals)
				addFieldNames(d.Type.Results, locals)
			}
		case *ast.FuncLit:
			if d.Type != nil {
				addFieldNames(d.Type.Params, locals)
				addFieldNames(d.Type.Results, locals)
			}
		case *ast.TypeSpec:
			locals[d.Name.Name] = true
		case *ast.ValueSpec:
			for _, id := range d.Names {
				locals[id.Name] = true
			}
		case *ast.AssignStmt:
			if d.Tok == token.DEFINE {
				for _, lhs := range d.Lhs {
					if id, ok := lhs.(*ast.Ident); ok {
						locals[id.Name] = true
					}
				}
			}
		}
		return true
	})
	return locals
}

func addFieldNames(fl *ast.FieldList, locals map[string]bool) {
	if fl == nil {
		return
	}
	for _, fld := range fl.List {
		for _, n := range fld.Names {
			locals[n.Name] = true
		}
	}
}

// indexHasSymbol reports whether the index contains a symbol named name,
// matching by exact Name or (for exported symbols) by FullName — the same
// lookup idiom Verify uses for bare identifiers.
func indexHasSymbol(ix *index.Index, name string) bool {
	if ix == nil {
		return false
	}
	for _, s := range ix.Symbols {
		if s.Name == name || (name == s.FullName() && isExported(s.Name)) {
			return true
		}
	}
	return false
}

// indexPackageSymbols returns every index symbol belonging to the package an
// import path refers to, and whether the package is known to the index. The
// path is matched exactly against index package paths and, failing that, by
// its last path segment (import paths carry module prefixes the index does
// not, e.g. "example.com/mod/db" -> indexed package "db"). Deterministic:
// candidate packages are collected in sorted key order.
func indexPackageSymbols(ix *index.Index, importPath string) ([]index.Symbol, bool) {
	if ix == nil {
		return nil, false
	}
	clean := strings.TrimPrefix(importPath, "./")
	base := clean
	if i := strings.LastIndex(base, "/"); i >= 0 {
		base = base[i+1:]
	}
	keys := slices.Sorted(maps.Keys(ix.Pkgs))
	var files []string
	matched := false
	for _, k := range keys {
		kb := k
		if i := strings.LastIndex(kb, "/"); i >= 0 {
			kb = kb[i+1:]
		}
		if k == clean || kb == base {
			matched = true
			files = append(files, ix.Pkgs[k].Files...)
		}
	}
	if !matched {
		return nil, false
	}
	fileSet := map[string]bool{}
	for _, f := range files {
		fileSet[f] = true
	}
	var out []index.Symbol
	for _, s := range ix.Symbols {
		if fileSet[s.File] {
			out = append(out, s)
		}
	}
	return out, true
}

var javaBuiltinKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"synchronized": true, "super": true, "this": true, "new": true,
	"return": true, "throw": true, "assert": true, "try": true,
}

var javaObjectMethods = map[string]bool{
	"equals": true, "hashCode": true, "toString": true, "getClass": true,
	"wait": true, "notify": true, "notifyAll": true, "clone": true, "finalize": true,
}

var javaExternalPrefixes = []string{
	"java.", "javax.", "jakarta.", "org.springframework.", "org.slf4j.",
	"org.junit.", "org.mockito.", "org.apache.", "com.google.", "com.fasterxml.",
	"io.micrometer.", "io.netty.", "io.swagger.", "lombok.", "org.hibernate.",
	"System.", "Arrays.", "Collections.", "Objects.", "Math.", "String.",
	"Integer.", "Long.", "Boolean.", "Double.", "Float.", "Thread.",
	"Optional.", "Stream.", "List.", "Set.", "Map.", "Assert.", "Assertions.",
}

func isExternalJavaPrefix(p string) bool {
	for _, ext := range javaExternalPrefixes {
		if p == strings.TrimSuffix(ext, ".") || strings.HasPrefix(p, ext) {
			return true
		}
	}
	return false
}

func looksLikeJava(code []byte) bool {
	s := string(code)
	if strings.Contains(s, "package ") && strings.Contains(s, ";") {
		return true
	}
	if strings.Contains(s, "public class ") || strings.Contains(s, "private class ") {
		return true
	}
	if strings.Contains(s, "class ") && strings.Contains(s, "{") && strings.Contains(s, ";") {
		return true
	}
	if strings.Contains(s, "import java.") || strings.Contains(s, "import javax.") || strings.Contains(s, "import org.") || strings.Contains(s, "import com.") {
		return true
	}
	if strings.Contains(s, "public static void main") || strings.Contains(s, "System.out.") {
		return true
	}
	return false
}

func checkJavaDraft(ix *index.Index, root string, code []byte) []DraftFinding {
	var findings []DraftFinding
	lines := strings.Split(string(code), "\n")

	locals := map[string]bool{}
	draftMethods := map[string]bool{}

	// First pass: collect local declarations
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		if m := javaClassRe.FindStringSubmatch(trimmed); m != nil {
			locals[m[1]] = true
		}
		if m := javaMethodRe.FindStringSubmatch(trimmed); m != nil {
			draftMethods[m[1]] = true
		}
		for _, m := range javaVarRe.FindAllStringSubmatch(trimmed, -1) {
			locals[m[2]] = true
		}
	}

	// Second pass: inspect calls
	for lineIdx, line := range lines {
		lineNum := lineIdx + 1
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") || strings.HasPrefix(trimmed, "*") {
			continue
		}
		cleanLine := stripStrings(trimmed)

		for _, m := range javaCallRe.FindAllStringSubmatch(cleanLine, -1) {
			full := m[1]
			if javaBuiltinKeywords[full] {
				continue
			}

			if strings.Contains(full, ".") {
				lastDot := strings.LastIndex(full, ".")
				receiver := full[:lastDot]
				method := full[lastDot+1:]

				if javaBuiltinKeywords[method] || javaObjectMethods[method] {
					continue
				}
				if isExternalJavaPrefix(receiver) {
					continue
				}
				if receiver == "this" || receiver == "super" {
					if draftMethods[method] {
						continue
					}
				}
				if locals[receiver] {
					continue
				}

				if ix != nil {
					// 1. Dotted method lookup (receiver match)
					if defs := ix.ResolveDottedMethod(receiver, method); len(defs) > 0 {
						continue
					}
					// 2. Exact or qualified symbol lookup
					if _, ok := ix.ResolveName(full); ok {
						continue
					}
					if _, ok := ix.FindSymbol(full); ok {
						continue
					}
					// 3. If receiver is a known class in index, check if method exists on it
					if sym, ok := ix.FindSymbol(receiver); ok {
						foundMethod := false
						for _, s := range ix.Symbols {
							if s.Name == method && (s.Receiver == sym.Name || s.Receiver == receiver) {
								foundMethod = true
								break
							}
						}
						if foundMethod {
							continue
						}
					}
				} else {
					if !strings.Contains(receiver, ".") {
						continue
					}
				}

				findings = append(findings, DraftFinding{
					Line:    lineNum,
					Kind:    "unknown_symbol",
					Message: fmt.Sprintf("call to unknown symbol %q", full),
				})
			} else {
				if javaBuiltinKeywords[full] || javaObjectMethods[full] {
					continue
				}
				if draftMethods[full] || locals[full] {
					continue
				}
				if ix != nil {
					if indexHasSymbol(ix, full) {
						continue
					}
					found := false
					for _, s := range ix.Symbols {
						if s.Name == full {
							found = true
							break
						}
					}
					if found {
						continue
					}
				} else {
					continue
				}
				findings = append(findings, DraftFinding{
					Line:    lineNum,
					Kind:    "unknown_symbol",
					Message: fmt.Sprintf("call to unknown symbol %q", full),
				})
			}
		}
	}

	return findings
}

func stripStrings(s string) string {
	var b strings.Builder
	inQuote := false
	var quoteChar rune
	escaped := false
	for _, r := range s {
		if inQuote {
			if escaped {
				escaped = false
			} else if r == '\\' {
				escaped = true
			} else if r == quoteChar {
				inQuote = false
			}
		} else {
			if r == '"' || r == '\'' {
				inQuote = true
				quoteChar = r
			} else {
				b.WriteRune(r)
			}
		}
	}
	return b.String()
}

func looksLikePython(code []byte) bool {
	s := string(code)
	if strings.Contains(s, "def ") && strings.Contains(s, ":") {
		return true
	}
	if strings.Contains(s, "import ") && (strings.Contains(s, "from ") || strings.Contains(s, "__name__")) {
		return true
	}
	if strings.Contains(s, "elif ") || strings.Contains(s, "except ") {
		return true
	}
	return false
}

func looksLikeJS(code []byte) bool {
	s := string(code)
	if (strings.Contains(s, "const ") || strings.Contains(s, "let ") || strings.Contains(s, "function ")) &&
		(strings.Contains(s, "=>") || strings.Contains(s, "export ") || strings.Contains(s, "import ") || strings.Contains(s, "require(")) {
		return true
	}
	return false
}

func looksLikeJSON(code []byte) bool {
	s := strings.TrimSpace(string(code))
	return (strings.HasPrefix(s, "{") && strings.HasSuffix(s, "}")) ||
		(strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]"))
}

func checkJSONDraft(code []byte) []DraftFinding {
	var v any
	err := json.Unmarshal(code, &v)
	if err == nil {
		return nil
	}
	line := 1
	if synErr, ok := err.(*json.SyntaxError); ok {
		line = bytes.Count(code[:synErr.Offset], []byte("\n")) + 1
	}
	return []DraftFinding{{
		Line:    line,
		Kind:    "parse_error",
		Message: err.Error(),
	}}
}

type bracketItem struct {
	r    rune
	line int
}

func checkBracketBalance(code []byte, commentPrefix string, blockCommentStart string, blockCommentEnd string) []DraftFinding {
	var findings []DraftFinding
	var stack []bracketItem
	lines := strings.Split(string(code), "\n")
	inBlockComment := false

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1
		inQuote := false
		var quoteChar rune
		escaped := false

		runes := []rune(line)
		for i := 0; i < len(runes); i++ {
			r := runes[i]

			if inBlockComment {
				if blockCommentEnd != "" && i+len(blockCommentEnd) <= len(runes) && string(runes[i:i+len(blockCommentEnd)]) == blockCommentEnd {
					inBlockComment = false
					i += len(blockCommentEnd) - 1
				}
				continue
			}

			if inQuote {
				if escaped {
					escaped = false
				} else if r == '\\' {
					escaped = true
				} else if r == quoteChar {
					inQuote = false
				}
				continue
			}

			if blockCommentStart != "" && i+len(blockCommentStart) <= len(runes) && string(runes[i:i+len(blockCommentStart)]) == blockCommentStart {
				inBlockComment = true
				i += len(blockCommentStart) - 1
				continue
			}

			if commentPrefix != "" && i+len(commentPrefix) <= len(runes) && string(runes[i:i+len(commentPrefix)]) == commentPrefix {
				break // rest of line is comment
			}

			if r == '"' || r == '\'' || r == '`' {
				inQuote = true
				quoteChar = r
				continue
			}

			if r == '(' || r == '[' || r == '{' {
				stack = append(stack, bracketItem{r: r, line: lineNum})
			} else if r == ')' || r == ']' || r == '}' {
				if len(stack) == 0 {
					findings = append(findings, DraftFinding{
						Line:    lineNum,
						Kind:    "parse_error",
						Message: fmt.Sprintf("unmatched closing bracket '%c'", r),
					})
					return findings
				}
				top := stack[len(stack)-1]
				stack = stack[:len(stack)-1]
				if (r == ')' && top.r != '(') || (r == ']' && top.r != '[') || (r == '}' && top.r != '{') {
					findings = append(findings, DraftFinding{
						Line:    lineNum,
						Kind:    "parse_error",
						Message: fmt.Sprintf("mismatched bracket '%c', expected match for '%c' from line %d", r, top.r, top.line),
					})
					return findings
				}
			}
		}
	}

	if len(stack) > 0 {
		top := stack[len(stack)-1]
		findings = append(findings, DraftFinding{
			Line:    top.line,
			Kind:    "parse_error",
			Message: fmt.Sprintf("unclosed bracket '%c'", top.r),
		})
	}
	return findings
}

func checkPythonDraft(ix *index.Index, root string, code []byte) []DraftFinding {
	var findings []DraftFinding
	bracketFindings := checkBracketBalance(code, "#", `"""`, `"""`)
	if len(bracketFindings) > 0 {
		return bracketFindings
	}

	lines := strings.Split(string(code), "\n")

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}

		if m := pyRelImportRe.FindStringSubmatch(trimmed); m != nil && root != "" {
			relDots := m[1]
			relPath := strings.ReplaceAll(relDots, ".", "/")
			fullPath := filepath.Join(root, relPath)
			if _, err := os.Stat(fullPath); err != nil {
				if _, err2 := os.Stat(fullPath + ".py"); err2 != nil {
					if _, err3 := os.Stat(filepath.Join(fullPath, "__init__.py")); err3 != nil {
						findings = append(findings, DraftFinding{
							Line:    lineNum,
							Kind:    "unknown_import",
							Message: fmt.Sprintf("relative import %q does not exist under root", relDots),
						})
					}
				}
			}
		}

		if pyColonHeaderRe.MatchString(trimmed) && !strings.HasSuffix(trimmed, ":") {
			findings = append(findings, DraftFinding{
				Line:    lineNum,
				Kind:    "parse_error",
				Message: fmt.Sprintf("expected ':' at end of header: %q", trimmed),
			})
		}
	}
	return findings
}

func checkJSDraft(ix *index.Index, root string, code []byte) []DraftFinding {
	var findings []DraftFinding
	bracketFindings := checkBracketBalance(code, "//", "/*", "*/")
	if len(bracketFindings) > 0 {
		return bracketFindings
	}

	lines := strings.Split(string(code), "\n")

	for lineIdx, line := range lines {
		lineNum := lineIdx + 1
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") || strings.HasPrefix(trimmed, "/*") {
			continue
		}

		if m := jsRelImportRe.FindStringSubmatch(trimmed); m != nil && root != "" {
			rel := m[1]
			if rel == "" && len(m) > 2 {
				rel = m[2]
			}
			if rel != "" && strings.HasPrefix(rel, ".") {
				base := filepath.Join(root, rel)
				candidates := []string{
					base,
					base + ".ts",
					base + ".tsx",
					base + ".js",
					base + ".jsx",
					base + ".mjs",
					filepath.Join(base, "index.ts"),
					filepath.Join(base, "index.js"),
				}
				found := false
				for _, c := range candidates {
					if _, err := os.Stat(c); err == nil {
						found = true
						break
					}
				}
				if !found {
					findings = append(findings, DraftFinding{
						Line:    lineNum,
						Kind:    "unknown_import",
						Message: fmt.Sprintf("relative import %q does not exist under root", rel),
					})
				}
			}
		}
	}
	return findings
}
