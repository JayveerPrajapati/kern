//go:build !notreesitter

package index

import (
	"bytes"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	"github.com/JayveerPrajapati/kern/internal/calleefilter"
	"github.com/JayveerPrajapati/kern/internal/langparse"
	"github.com/UserNobody14/tree-sitter-dart/bindings/go"
	sitter "github.com/tree-sitter/go-tree-sitter"
	"github.com/tree-sitter/tree-sitter-bash/bindings/go"
	"github.com/tree-sitter/tree-sitter-c/bindings/go"
	"github.com/tree-sitter/tree-sitter-cpp/bindings/go"
	"github.com/tree-sitter/tree-sitter-css/bindings/go"
	"github.com/tree-sitter/tree-sitter-go/bindings/go"
	"github.com/tree-sitter/tree-sitter-java/bindings/go"
	"github.com/tree-sitter/tree-sitter-javascript/bindings/go"
	"github.com/tree-sitter/tree-sitter-php/bindings/go"
	"github.com/tree-sitter/tree-sitter-python/bindings/go"
	"github.com/tree-sitter/tree-sitter-ruby/bindings/go"
	"github.com/tree-sitter/tree-sitter-rust/bindings/go"
	"github.com/tree-sitter/tree-sitter-typescript/bindings/go"
)

// treesitterEnabled reports whether tree-sitter extraction is compiled in.
func treesitterEnabled() bool { return true }

// TreesitterEnabled reports whether the tree-sitter extractor is available in
// this build. The default build includes tree-sitter; -tags notreesitter opts
// out and falls back to regex heuristics.
func TreesitterEnabled() bool { return treesitterEnabled() }

// tsLanguageMap maps kern language IDs to tree-sitter Language pointers.
var tsLanguageMap = map[string]*sitter.Language{"go": sitter.NewLanguage(tree_sitter_go.Language()),
	"python":     sitter.NewLanguage(tree_sitter_python.Language()),
	"javascript": sitter.NewLanguage(tree_sitter_javascript.Language()),
	"typescript": sitter.NewLanguage(tree_sitter_typescript.LanguageTypescript()),
	"tsx":        sitter.NewLanguage(tree_sitter_typescript.LanguageTSX()),
	"c":          sitter.NewLanguage(tree_sitter_c.Language()),
	"cpp":        sitter.NewLanguage(tree_sitter_cpp.Language()),
	"java":       sitter.NewLanguage(tree_sitter_java.Language()),
	"rust":       sitter.NewLanguage(tree_sitter_rust.Language()),
	"ruby":       sitter.NewLanguage(tree_sitter_ruby.Language()),
	"php":        sitter.NewLanguage(tree_sitter_php.LanguagePHP()),
	"bash":       sitter.NewLanguage(tree_sitter_bash.Language()),
	"css":        sitter.NewLanguage(tree_sitter_css.Language()),
	"dart":       sitter.NewLanguage(tree_sitter_dart.Language()),
}

// tsNodeTypes maps tree-sitter node types to kern symbol kinds. Keys are
// grammar node types shared across languages; several languages emit the same
// node type names, so each appears once.
var tsNodeTypes = map[string]string{
	// Go (unused in practice — Go uses the AST extractor)
	"function_declaration": "func",
	"method_declaration":   "method",
	"type_declaration":     "type",
	"var_declaration":      "var",
	"const_declaration":    "const",
	"struct_type":          "struct",
	"interface_type":       "interface",
	// JavaScript/TypeScript
	"function_expression":            "func",
	"generator_function_declaration": "func",
	// NOTE: arrow_function is deliberately NOT a symbol node. Anonymous
	// arrows have no name of their own — ExtractName's child scan would
	// return the FIRST PARAMETER ("n" in `xs.map(n => n * 2)`), minting a
	// phantom method named "n" per callback that collides on the duplicate
	// gate when the same parameter name repeats inside one enclosing
	// function (live repro: wf_project conduit AIAssistantPanel.tsx, two
	// `.map(n => ...)` callbacks, index refused). Named bindings keep
	// their symbol via the const/let/var declaration path, which promotes
	// to func when it has an arrow_function descendant (below).
	"method_definition":          "method",
	"class_declaration":          "class",
	"class":                      "class",
	"abstract_class_declaration": "class",
	"interface_declaration":      "interface",
	"type_alias_declaration":     "type",
	"enum_declaration":           "enum",
	"variable_declaration":       "var",
	"lexical_declaration":        "const",
	"function_signature":         "func",
	"method_signature":           "method",
	"property_signature":         "prop",
	"public_field_definition":    "prop",
	"abstract_method_signature":  "method",
	// Python
	"function_definition":       "func",
	"class_definition":          "class",
	"async_function_definition": "func",
	"type_alias_statement":      "type",
	// Rust
	"function_item":           "func",
	"function_signature_item": "func",
	"struct_item":             "struct",
	"enum_item":               "enum",
	"trait_item":              "trait",
	"impl_item":               "impl",
	"const_item":              "const",
	"static_item":             "var",
	"type_item":               "type",
	"union_item":              "union",
	"mod_item":                "module",
	// Java / C# / PHP (shared declaration names)
	"record_declaration":          "record",
	"constructor_declaration":     "method",
	"field_declaration":           "var",
	"annotation_type_declaration": "type",
	"struct_declaration":          "struct",
	"namespace_declaration":       "namespace",
	"trait_declaration":           "trait",
	"property_declaration":        "prop",
	// C/C++
	"class_specifier":      "class",
	"struct_specifier":     "struct",
	"enum_specifier":       "enum",
	"union_specifier":      "union",
	"namespace_definition": "namespace",
	"type_definition":      "type",
	// Ruby
	"method":           "method",
	"singleton_method": "method",
	"module":           "module",
	// Bash
	"variable_assignment": "var",
	// Dart
	"mixin_declaration":             "class",
	"extension_declaration":         "class",
	"getter_signature":              "method",
	"setter_signature":              "method",
	"constructor_signature":         "method",
	"factory_constructor_signature": "method",
	"operator_signature":            "method",
	"local_function_declaration":    "func",
	"type_alias":                    "type",
	// CSS
	"class_selector":      "class",
	"id_selector":         "const",
	"keyframes_statement": "func",
	"property_name":       "prop",
}

// tsLanguageFor maps kern language ids (from langparse.DetectLang) to tree-sitter
// grammar ids. langparse.DetectLang uses "shell" for shell files and "typescript" for
// both .ts and .tsx; the grammars are keyed "bash" and "tsx" respectively.
func tsLanguageFor(lang, rel string) (string, bool) {
	switch lang {
	case "shell":
		return "bash", true
	case "typescript":
		if strings.HasSuffix(rel, ".tsx") || strings.Contains(rel, ".tsx.") {
			return "tsx", true
		}
		return "typescript", true
	}
	_, ok := tsLanguageMap[lang]
	return lang, ok
}

// tsParserPool recycles tree-sitter parsers across files: sitter.NewParser
// is a C allocation, and allocating one per foreign-language file (plus
// Close per file) was pure churn on polyglot trees. Parsers are safe to
// reuse after Parse — the language is re-armed per use via SetLanguage —
// and pooled parsers are never Close()d: the pool keeps a bounded set
// alive for the process lifetime instead.
var tsParserPool = sync.Pool{
	New: func() any { return sitter.NewParser() },
}

// tsExtractor uses tree-sitter to extract symbols, calls and inheritance.
func tsExtract(rel string, src []byte, lang string) ([]Symbol, map[string][]CallEdge, map[string][]string, *Pkg, error) {
	tsLangID, ok := tsLanguageFor(lang, rel)
	if !ok {
		return nil, nil, nil, nil, fmt.Errorf("no tree-sitter grammar for %s", lang)
	}
	tsLang := tsLanguageMap[tsLangID]

	parser := tsParserPool.Get().(*sitter.Parser)
	defer tsParserPool.Put(parser)
	if err := parser.SetLanguage(tsLang); err != nil {
		return nil, nil, nil, nil, err
	}

	tree := parser.Parse(src, nil)
	if tree == nil {
		return nil, nil, nil, nil, fmt.Errorf("parse failed")
	}
	defer tree.Close()

	root := tree.RootNode()
	if root.IsError() || root.HasError() {
		return nil, nil, nil, nil, fmt.Errorf("parse has errors")
	}

	calls := map[string][]CallEdge{}

	// First pass: collect all definitions
	defs := collectDefinitions(root, src, rel, lang)

	// Second pass: collect call edges
	collectCalls(root, src, defs, calls, lang, rel)

	// Java: resolve member-invocation callees through per-method local types
	// (x.bar() -> Foo.bar()) so cross-file edges bind (java_resolve.go).
	if lang == "java" {
		langparse.ResolveJavaCalls(root, src, calls)
	}

	// Third pass: collect inheritance (extends / implements) edges
	inherits := collectInheritance(root, src, defs, lang)

	// Build package info
	pkg := &Pkg{
		Name:  filepath.Base(filepath.Dir(rel)),
		Path:  filepath.Dir(rel),
		Files: []string{rel},
		Lang:  lang,
	}

	calls = langparse.AttributeTopLevelCalls(rel, src, lang, defs, calls)
	// Top-level (file-owned) edges enter at MEDIUM (the shared regex
	// entry-rule scan); where the AST proved the callee is a real symbol in
	// this file, the edge inherits AST-grade confidence.
	promoteFileEdgesToASTConfidence(rel, defs, calls)
	return defs, calls, inherits, pkg, nil
}

// promoteFileEdgesToASTConfidence raises MEDIUM file-owned edges (top-level
// calls from the shared regex scan in langparse.AttributeTopLevelCalls) to HIGH when
// the callee is a verified tree-sitter symbol: the AST has proven the
// target exists in this file (recommendation B part 2). Only exact
// full-name matches are promoted; dotted targets through unknown receivers
// and external callees stay MEDIUM (regex is never more certain).
func promoteFileEdgesToASTConfidence(rel string, defs []Symbol, calls map[string][]CallEdge) {
	owner := "file:" + rel
	edges := calls[owner]
	if len(edges) == 0 {
		return
	}
	known := make(map[string]bool, len(defs)*2)
	for _, d := range defs {
		known[d.FullName()] = true
		// Only file-scope, non-method symbols promote by bare Name. A method's
		// short name (Greeter.greet -> "greet") must NOT promote an external or
		// top-level callee that merely shares the name — the AST verified the
		// qualified symbol, not the bare call target.
		if d.Receiver == "" {
			known[d.Name] = true
		}
	}
	for i := range edges {
		if edges[i].Confidence != ConfidenceMedium {
			continue
		}
		if known[edges[i].Target] {
			edges[i].Confidence = ConfidenceHigh
		}
	}
}

// isPythonOverloadStub reports whether a Python decorated_definition wraps a
// function whose decorator stack contains a literal `overload` or
// `typing.overload` — the typing stub idiom (multiple @overload signatures
// followed by one real implementation). Stubs must not claim the symbol
// identity: the real def owns it, and a stub colliding with the impl trips
// the fail-loud duplicate gate. Robust across stacked decorators (@overload
// combined with @staticmethod or others): ANY decorator in the stack matching
// the literal names marks the def as a stub. Only function definitions
// qualify — @overload on a class is not the typing idiom and keeps
// extracting.
func isPythonOverloadStub(n *sitter.Node, src []byte) bool {
	isFunc := false
	hasOverload := false
	for i := uint(0); i < n.ChildCount(); i++ {
		c := n.Child(i)
		if c == nil {
			continue
		}
		switch c.Kind() {
		case "decorator":
			if !hasOverload && decoratorIsOverload(c, src) {
				hasOverload = true
			}
		case "function_definition":
			isFunc = true
		}
	}
	return isFunc && hasOverload
}

// decoratorIsOverload reports whether a decorator node is literally
// `overload` or `typing.overload`: the decorated name's text (the decorator's
// non-`@` child — identifier, attribute, …) is one of the two names.
func decoratorIsOverload(d *sitter.Node, src []byte) bool {
	for i := uint(0); i < d.ChildCount(); i++ {
		e := d.Child(i)
		if e == nil || e.Kind() == "@" {
			continue
		}
		name := string(src[e.StartByte():e.EndByte()])
		return name == "overload" || name == "typing.overload"
	}
	return false
}

// collectDefinitions walks the AST and extracts symbol definitions.
func collectDefinitions(node *sitter.Node, src []byte, file, lang string) []Symbol {
	var syms []Symbol
	// CSS is redeclarative: the same class/id/property is legitimately
	// re-selected in compound (`.data-table th`), pseudo (`.btn:disabled`)
	// and media contexts on different lines — the fail-loud duplicate gate
	// (correct for code languages) must not fire on valid CSS. A CSS symbol
	// is its selector NAME within a file: keep the first occurrence as the
	// definition site, drop later re-selections. @keyframes names (func)
	// stay gated — a repeated name is a real CSS error. The regex extractor
	// dedupes identically (foreign_lang.go) so tree-sitter/regex parity
	// holds.
	cssSeen := map[string]bool{}
	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		kind := n.Kind()
		// Dart nests function_signature inside method_signature (and, for
		// abstract members, abstract_method_signature). Both node kinds map
		// to a symbol, so emitting the inner signature mints every method
		// twice (live: 1,875 symbols for 12 Dart files, every method
		// duplicated). The enclosing method node owns the identity.
		if kind == "function_signature" {
			for p := n.Parent(); p != nil; p = p.Parent() {
				switch p.Kind() {
				case "method_signature", "abstract_method_signature", "method_definition",
					"getter_signature", "setter_signature":
					return
				}
			}
		}
		// Python typing stubs: skip @overload-decorated defs wholesale — the
		// signature carries no body and the real implementation (defined
		// separately) owns the identity. Without this, the stub and the impl
		// collide on the fail-loud duplicate gate ("same identity extracted
		// twice").
		if lang == "python" && kind == "decorated_definition" && isPythonOverloadStub(n, src) {
			return
		}
		if symKind, ok := tsNodeTypes[kind]; ok {
			name := langparse.ExtractName(n, src, kind)
			// Dart getters and setters share a name; without a distinct
			// identity the duplicate gate refuses the file. Name the
			// setter `<name>=` (the Dart convention) so index succeeds
			// while both members stay searchable.
			if name != "" && lang == "dart" && dartSetter(n, src) {
				name += "="
			}
			// Skip the file root node (Python's "module"): a container.
			if name != "" && n != node {
				// Only CSS custom properties (--*) are props worth indexing.
				if kind == "property_name" && !strings.HasPrefix(name, "--") {
					return
				}
				if lang == "css" && symKind != "func" {
					key := symKind + "\x00" + name
					if cssSeen[key] {
						return
					}
					cssSeen[key] = true
				}
				start := n.StartPosition()
				end := n.EndPosition()
				sym := Symbol{
					Kind: symKind,
					Name: name,
					File: file,
					Line: int(start.Row) + 1,
					End:  int(end.Row) + 1,
					Lang: lang,
				}
				// Direct declarations score HIGH; CSS id-selector "const" is LOW.
				sym.Confidence = ConfidenceHigh
				if symKind == "const" && kind == "id_selector" {
					sym.Confidence = ConfidenceLow
				}
				// JS/TS arrow functions via const/let/var read as funcs (regex parity).
				if symKind == "const" || symKind == "var" {
					if hasDescendantKind(n, "arrow_function") {
						sym.Kind = "func"
						sym.Confidence = ConfidenceMedium
					}
				}
				// C++ out-of-class names ("Shape::Shape"): split receiver.
				if i := strings.Index(name, "::"); i >= 0 {
					sym.Receiver = name[:i]
					sym.Name = name[i+2:]
					sym.Kind = "method"
				}
				// Nested types record their enclosing class in Outer — the
				// conflict-check discriminator for same-named nested classes
				// under different parents (WD-6c).
				if symKind != "method" && symKind != "func" {
					sym.Outer = langparse.FindReceiver(n, node, src)
				}
				// Find the receiver for methods; promote class-contained funcs.
				if symKind == "method" || symKind == "func" {
					if recv := langparse.FindReceiver(n, node, src); recv != "" {
						sym.Kind = "method"
						sym.Receiver = recv
						// The receiver class's own parent (WD-6d): same-named
						// nested classes hold same-named methods.
						sym.Outer = outerOfClass(n, node, src)
					} else if symKind == "method" {
						sym.Kind = "func"
					}
					// Overload discriminator (WD-6): different parameter
					// lists = legitimate overloads, not duplicates.
					for i := uint(0); i < n.ChildCount(); i++ {
						fp := n.Child(i)
						if fp == nil || fp.Kind() != "formal_parameters" {
							continue
						}
						for j := uint(0); j < fp.ChildCount(); j++ {
							if p := fp.Child(j); p != nil && (p.Kind() == "formal_parameter" || p.Kind() == "required_parameter") {
								sym.Params = append(sym.Params, string(src[p.StartByte():p.EndByte()]))
							}
						}
						break
					}
				}
				syms = append(syms, sym)
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if child := n.Child(i); child != nil {
				walk(child)
			}
		}
	}
	walk(node)
	return syms
}

// collectCalls walks the AST and extracts call edges.
func collectCalls(node *sitter.Node, src []byte, defs []Symbol, calls map[string][]CallEdge, lang string, file string) {
	// Build a set of locally-defined symbol names so calls to them are always
	// kept even when the regex keyword heuristic would not match.
	nameSet := make(map[string]bool)
	for _, d := range defs {
		nameSet[d.FullName()] = true
		nameSet[d.Name] = true
	}

	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		kind := n.Kind()
		// Look for call expressions, bash commands whose first word names a
		// local function, and object/type creations (Java `new App(1)`).
		isCall := strings.HasSuffix(kind, "call") || strings.HasSuffix(kind, "invocation") ||
			kind == "call_expression" || kind == "command" || kind == "object_creation_expression" ||
			kind == "new_expression" || kind == "function_expression" || kind == "selector"
		// Dart calls are identifier + selector chains; only selectors that end in
		// an argument list are calls ("a[0]" index access is not).
		if kind == "selector" && !hasDescendantKind(n, "argument_part") && !hasDescendantKind(n, "arguments") {
			isCall = false
		}
		if isCall {
			// Try to extract the callee name
			if callee := langparse.ExtractCallee(n, src); callee != "" {
				// Find the enclosing function
				if caller := findEnclosingFunction(n, src); caller != "" {
					if lang != "go" || !calleefilter.IsNoiseCalleeFor(callee, true, file) {
						if _, ok := nameSet[callee]; ok || !isKeywordCall(callee) {
							if callee != caller {
								// Calls to a locally-defined symbol are direct
								// syntactic edges: HIGH. Unresolved targets (not
								// in the local definition set) are name-based
								// guesses that may be external or dynamic: MEDIUM.
								conf := ConfidenceHigh
								if _, ok := nameSet[callee]; !ok {
									conf = ConfidenceMedium
								}
								calls[caller] = append(calls[caller], CallEdge{Target: callee, Confidence: conf})
							}
						}
					}
				}
			}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if child := n.Child(i); child != nil {
				walk(child)
			}
		}
	}
	walk(node)
}

// collectInheritance walks the AST and extracts extends / implements edges.
// Results are keyed by the subtype's symbol name; values carry the edge kind
// prefix ("extends:Base", "implements:IFace") so queries can distinguish
// inheritance from interface implementation.
func collectInheritance(node *sitter.Node, src []byte, defs []Symbol, lang string) map[string][]string {
	inherits := map[string][]string{}

	// collectNamesDeep returns every name-bearing descendant's text under n,
	// recursing through wrapper nodes (type_list, class_heritage, ...).
	var collectNamesDeep func(n *sitter.Node) []string
	collectNamesDeep = func(n *sitter.Node) []string {
		var out []string
		switch n.Kind() {
		case "type_arguments", "type_parameters":
			return nil
		case "identifier", "type_identifier", "constant", "name", "namespace_name",
			"class_name", "scoped_identifier", "qualified_identifier":
			return []string{string(src[n.StartByte():n.EndByte()])}
		}
		for i := uint(0); i < n.ChildCount(); i++ {
			if c := n.Child(i); c != nil {
				out = append(out, collectNamesDeep(c)...)
			}
		}
		return out
	}

	var walk func(*sitter.Node)
	walk = func(n *sitter.Node) {
		kind := n.Kind()
		subtype := ""
		var bases []string

		switch kind {
		case "class_definition": // Python: class Cat(Animal); Dart: class Cat extends Animal implements Pet
			subtype = langparse.ExtractName(n, src, kind)
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c == nil {
					continue
				}
				switch c.Kind() {
				case "argument_list":
					bases = append(bases, tag("extends", collectNamesDeep(c))...)
				case "superclass":
					bases = append(bases, tag("extends", collectNamesDeep(c))...)
				case "interfaces":
					bases = append(bases, tag("implements", collectNamesDeep(c))...)
				}
			}
		case "class_declaration", "interface_declaration": // JS/TS/PHP/Java
			subtype = langparse.ExtractName(n, src, kind)
			extendsKinds := map[string]bool{
				"extends_clause": true, "superclass": true, "extends_interfaces": true,
				"extends_type_clause": true, "base_clause": true, "base_class_clause": true,
				"class_heritage": true,
			}
			implementsKinds := map[string]bool{
				"implements_clause": true, "super_interfaces": true, "class_interface_clause": true,
			}
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c == nil {
					continue
				}
				ck := c.Kind()
				switch {
				case extendsKinds[ck]:
					// class_heritage nests extends/implements clauses; unwrap it
					// so each clause is classified by its own kind.
					clauses := 0
					for j := uint(0); j < c.ChildCount(); j++ {
						cc := c.Child(j)
						if cc == nil {
							continue
						}
						cck := cc.Kind()
						if extendsKinds[cck] {
							clauses++
							bases = append(bases, tag("extends", collectNamesDeep(cc))...)
						} else if implementsKinds[cck] {
							clauses++
							bases = append(bases, tag("implements", collectNamesDeep(cc))...)
						}
					}
					// JavaScript's class_heritage holds the base expression
					// directly, with no extends_clause wrapper.
					if ck == "class_heritage" && clauses == 0 {
						bases = append(bases, tag("extends", collectNamesDeep(c))...)
					}
					if ck != "class_heritage" {
						bases = append(bases, tag("extends", collectNamesDeep(c))...)
					}
				case implementsKinds[ck]:
					bases = append(bases, tag("implements", collectNamesDeep(c))...)
				}
			}
		case "class": // Ruby: class Cat < Animal
			subtype = langparse.ExtractName(n, src, kind)
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c != nil && c.Kind() == "superclass" {
					bases = append(bases, tag("extends", collectNamesDeep(c))...)
				}
			}
		case "class_specifier": // C++: class Cat : public Animal, virtual Pet
			subtype = langparse.ExtractName(n, src, kind)
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c != nil && c.Kind() == "base_class_clause" {
					bases = append(bases, tag("extends", collectNamesDeep(c))...)
				}
			}
		case "impl_item": // Rust: impl Animal for Cat
			names := collectNamesDeep(n)
			if len(names) >= 2 {
				subtype = names[len(names)-1]
				bases = append(bases, tag("implements", names[:len(names)-1])...)
			}
		case "trait_item": // Rust: trait Meow: Speak
			subtype = langparse.ExtractName(n, src, kind)
			for i := uint(0); i < n.ChildCount(); i++ {
				c := n.Child(i)
				if c != nil && c.Kind() == "trait_bounds" {
					bases = append(bases, tag("extends", collectNamesDeep(c))...)
				}
			}
		}

		if subtype != "" && len(bases) > 0 {
			for _, b := range bases {
				if b != "" && b != subtype {
					inherits[subtype] = append(inherits[subtype], b)
				}
			}
		}

		for i := uint(0); i < n.ChildCount(); i++ {
			if child := n.Child(i); child != nil {
				walk(child)
			}
		}
	}
	walk(node)
	return inherits
}

// tag prefixes every base name with the given edge kind.
func tag(kind string, names []string) []string {
	out := make([]string, 0, len(names))
	for _, n := range names {
		out = append(out, kind+":"+n)
	}
	return out
}

// isKeywordCall reports whether a bare callee name looks like a language
// keyword or control-flow word rather than a user symbol, so it is not
// recorded as a call edge.
func isKeywordCall(name string) bool {
	kw := map[string]bool{
		"if": true, "else": true, "elif": true, "for": true, "while": true, "switch": true,
		"case": true, "return": true, "break": true, "continue": true, "yield": true,
		"new": true, "delete": true, "typeof": true, "instanceof": true, "in": true,
		"of": true, "await": true, "async": true, "try": true, "catch": true, "finally": true,
		"throw": true, "raise": true, "pass": true, "import": true, "export": true, "from": true,
		"print": true, "exit": true, "assert": true, "super": true, "this": true,
		"then": true, "do": true, "def": true, "fn": true, "func": true, "function": true,
		"match": true, "with": true, "as": true, "struct": true, "enum": true, "trait": true,
		"impl": true, "let": true, "mut": true, "const": true, "var": true, "static": true,
		"class": true, "interface": true, "echo": true, "printf": true, "puts": true,
	}
	return kw[name]
}

// hasDescendantKind reports whether any node at or below n has the given kind.
func hasDescendantKind(n *sitter.Node, want string) bool {
	if n.Kind() == want {
		return true
	}
	for i := uint(0); i < n.ChildCount(); i++ {
		if child := n.Child(i); child != nil && hasDescendantKind(child, want) {
			return true
		}
	}
	return false
}

// langparse.FindReceiver finds the receiver/container type for a method. Rust methods
// live under impl_item; class methods under a class body whose ancestor
// carries the type name; Python under class_definition; PHP class_declaration.
// outerOfClass returns the class enclosing the symbol's immediate class
// container — the receiver class's own parent (WD-6d discriminator).
// Empty when the container is top-level.
func outerOfClass(n, root *sitter.Node, src []byte) string {
	for p := n.Parent(); p != nil && p.Id() != root.Id(); p = p.Parent() {
		switch p.Kind() {
		case "class_definition", "class_declaration", "interface_declaration",
			"enum_declaration", "record_declaration", "struct_declaration",
			"trait_declaration", "struct_item", "trait_item", "impl_item":
			return langparse.FindReceiver(p, root, src)
		}
	}
	return ""
}

// dartSetter reports whether a Dart method_signature declares a setter
// (`set name(...)`), which must not share the getter's identity.
func dartSetter(n *sitter.Node, src []byte) bool {
	start := int(n.StartByte())
	// The "set" keyword may be a sibling token outside the signature node, so
	// look at the text immediately before it on the same line first.
	lineStart := bytes.LastIndexByte(src[:start], '\n') + 1
	if prefix := strings.TrimSpace(string(src[lineStart:start])); strings.HasSuffix(prefix, "set") {
		return true
	}
	text := string(src[start:n.EndByte()])
	if i := strings.IndexByte(text, '('); i >= 0 {
		text = text[:i]
	}
	fields := strings.Fields(text)
	for i, f := range fields {
		if f == "set" && i+1 < len(fields) {
			return true
		}
	}
	return false
}

// findEnclosingFunction finds the function containing a call and returns its
// receiver-qualified name (e.g. "User.login") so call edges land on the same
// key the definition uses.
// bodyKinds are container nodes whose previous sibling can carry a function
// signature (Dart keeps "function_signature" and "function_body" as siblings
// under program / class_body).
var bodyKinds = map[string]bool{
	"function_body": true, "compound_statement": true, "block": true,
	"class_body": true, "declaration_list": true, "body": true,
}

func findEnclosingFunction(node *sitter.Node, src []byte) string {
	for p := node.Parent(); p != nil; p = p.Parent() {
		k := p.Kind()
		// Dart keeps the signature and the body as siblings under the
		// enclosing list (program / class_body): a body node's previous
		// sibling carries the signature.
		if bodyKinds[k] {
			if ps := p.PrevSibling(); ps != nil && langparse.FuncKinds[ps.Kind()] {
				if name := langparse.ExtractName(ps, src, ps.Kind()); name != "" {
					// C++ qualified definitions ("Shape::area") map to "Shape.area".
					if i := strings.Index(name, "::"); i >= 0 {
						return name[:i] + "." + name[i+2:]
					}
					if recv := langparse.FindReceiver(ps, langparse.RootOf(ps), src); recv != "" {
						return recv + "." + name
					}
					return name
				}
			}
		}
		if langparse.FuncKinds[k] {
			if name := langparse.ExtractName(p, src, k); name != "" {
				// C++ qualified definitions ("Shape::area") map to "Shape.area".
				if i := strings.Index(name, "::"); i >= 0 {
					return name[:i] + "." + name[i+2:]
				}
				if recv := langparse.FindReceiver(p, langparse.RootOf(p), src); recv != "" {
					return recv + "." + name
				}
				return name
			}
		}
	}
	return ""
}
func TreeSitterAvailable(lang string) bool {
	if _, ok := tsLanguageFor(lang, ""); ok {
		return true
	}
	return false
}

// TsExtract exposes the tree-sitter extractor to sibling packages (langparse
// injects it into ExtractForeign) and to external tests. Same behavior as
// tsExtract; the stub build returns the not-enabled error.
func TsExtract(rel string, src []byte, lang string) ([]Symbol, map[string][]CallEdge, map[string][]string, *Pkg, error) {
	return tsExtract(rel, src, lang)
}
